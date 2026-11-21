package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/shared/names"
)

// ServerBinary is the walroos binary copied into static sites' images: the node agent itself.
// Tests point it at a build of cmd/walroos.
var ServerBinary = os.Executable

// Each runtime's image and its default version, which NODE_VERSION or BUN_VERSION (or the
// repo's .nvmrc or .node-version, for Node) change, like on Netlify.
var runtimes = map[string]struct{ image, version, env string }{
	"node": {"docker.io/library/node", "24", "NODE_VERSION"},
	"bun":  {"docker.io/oven/bun", "1", "BUN_VERSION"},
}

const plainImage = "docker.io/library/busybox:stable" // plain HTML: only copied

// versionRe is what a version may be: 22, 22.11, 22.11.0, lts or latest (an image tag part).
var versionRe = regexp.MustCompile(`^([0-9]{1,3}(\.[0-9]{1,3}){0,2}|lts|latest)$`)

// Package managers' download caches, kept between builds on the node (podman cache mounts).
var pmCaches = map[string]string{
	"npm":  "/root/.npm",
	"pnpm": "/root/.local/share/pnpm/store",
	"yarn": "/usr/local/share/.cache/yarn",
	"bun":  "/root/.bun/install/cache",
}

// staticPlan is how a static site builds: in which image, with which commands, served from where.
type staticPlan struct {
	runtime, pm    string // "" for plain HTML: nothing to install or build
	version        string // the runtime's version: an image tag part
	versionFrom    string // where the version came from, for the log
	install, build string
	publish        string   // set by the owner; else the first of candidates with an index.html
	candidates     []string // where builds usually leave the site
	spa            bool
}

// planStatic reads the repo at dir: its lockfile (or BUN_VERSION in env) picks the runtime and
// the package manager, package.json's scripts the build. What b sets wins.
func planStatic(dir string, b agent.StaticBuild, env []string) staticPlan {
	has := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	p := staticPlan{install: b.Install, build: b.Build, publish: b.Publish, spa: b.SPA}
	if has("package.json") || b.Install != "" || b.Build != "" || b.Runtime != "" {
		p.runtime = b.Runtime
		if p.runtime == "" {
			p.runtime = "node"
			if has("bun.lock") || has("bun.lockb") || envValue(env, "BUN_VERSION") != "" {
				p.runtime = "bun"
			}
		}
		p.pm = packageManager(p.runtime, has)
		p.version, p.versionFrom = runtimeVersion(dir, p.runtime, env)
	}
	if p.install == "" && has("package.json") {
		p.install = installCommand(p.pm, has)
	}
	if p.build == "" && hasBuildScript(dir) {
		p.build = p.pm + " run build"
	}
	p.candidates = []string{"dist/*/browser", "dist", "build", "out", "public", "_site"}
	if p.build == "" {
		p.candidates = append(p.candidates, ".") // a plain site is its repo; a built one never serves its sources
	}
	return p
}

// runtimeVersion: NODE_VERSION or BUN_VERSION, then (Node) .nvmrc or .node-version, then the
// default. A value that isn't a version is skipped; the log says which one was used.
func runtimeVersion(dir, runtime string, env []string) (version, from string) {
	r := runtimes[runtime]
	values, sources := []string{envValue(env, r.env)}, []string{r.env}
	if runtime == "node" {
		for _, f := range []string{".nvmrc", ".node-version"} {
			b, _ := os.ReadFile(filepath.Join(dir, f))
			values, sources = append(values, string(b)), append(sources, f)
		}
	}
	for i, v := range values {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		if strings.HasPrefix(v, "lts/") { // .nvmrc's lts/* or lts/iron: the current LTS
			v = "lts"
		}
		if versionRe.MatchString(v) {
			return v, sources[i]
		}
	}
	return r.version, "default"
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// image is the build stage's image.
func (p staticPlan) image() string {
	if p.runtime == "" {
		return plainImage
	}
	tag := p.version + "-slim"
	if p.version == "latest" {
		tag = "slim"
	}
	return runtimes[p.runtime].image + ":" + tag
}

func packageManager(runtime string, has func(string) bool) string {
	switch {
	case runtime == "bun":
		return "bun"
	case has("pnpm-lock.yaml"):
		return "pnpm"
	case has("yarn.lock"):
		return "yarn"
	}
	return "npm"
}

func installCommand(pm string, has func(string) bool) string {
	switch pm {
	case "pnpm":
		return "pnpm install --frozen-lockfile"
	case "yarn":
		return "yarn install --frozen-lockfile"
	case "bun":
		if has("bun.lock") || has("bun.lockb") {
			return "bun install --frozen-lockfile"
		}
		return "bun install"
	}
	if has("package-lock.json") || has("npm-shrinkwrap.json") {
		return "npm ci"
	}
	return "npm install"
}

func hasBuildScript(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	return json.Unmarshal(b, &pkg) == nil && pkg.Scripts["build"] != ""
}

// script runs in the build stage, in the repo: env vars, install, build, then the site to /out.
func (p staticPlan) script() string {
	var b strings.Builder
	b.WriteString("set -e\nif [ -f /run/secrets/env ]; then . /run/secrets/env; fi\n")
	if p.pm == "pnpm" {
		b.WriteString("corepack enable pnpm 2>/dev/null || npm install --global --silent pnpm\n")
	}
	for _, step := range [][2]string{{"install", p.install}, {"build", p.build}} {
		if step[1] != "" {
			fmt.Fprintf(&b, "echo %s\n%s\n", shellQuote("--> "+step[0]+": "+step[1]), step[1])
		}
	}
	if p.publish != "" {
		fmt.Fprintf(&b, "d=%s\n[ -d \"$d\" ] || { echo \"walroos: no folder $d after the build; check the publish directory\" >&2; exit 1; }\n", shellQuote(p.publish))
	} else {
		fmt.Fprintf(&b, "for d in %s; do [ -f \"$d/index.html\" ] && break; d=; done\n", strings.Join(p.candidates, " "))
		fmt.Fprintf(&b, "[ -n \"$d\" ] || { echo %s >&2; exit 1; }\n",
			shellQuote("walroos: found no index.html in "+strings.Join(p.candidates, ", ")+"; set the publish directory"))
	}
	b.WriteString("echo \"--> serving $d\"\nmkdir -p /out\ncp -a \"$d\"/. /out/\nrm -rf /out/.git /out/node_modules\n")
	return b.String()
}

// dockerfile builds in the runtime's image and keeps only the site and the static server.
// The repo is the main context; "walroos" is ours (the binary and the script), and the env
// vars come in as a secret, so they're in no layer.
func (p staticPlan) dockerfile() string {
	mounts := "--mount=type=secret,id=env"
	if c := pmCaches[p.pm]; c != "" {
		mounts += " --mount=type=cache,target=" + c
	}
	entry := []string{"/walroos", "static-server", "--dir", "/site", "--port", strconv.Itoa(agent.StaticPort)}
	if p.spa {
		entry = append(entry, "--spa")
	}
	quoted, _ := json.Marshal(entry)
	return fmt.Sprintf(`FROM %s AS build
WORKDIR /src
COPY . .
COPY --from=walroos build.sh /walroos-build.sh
RUN %s sh /walroos-build.sh

FROM scratch
COPY --from=walroos walroos /walroos
COPY --from=build /out /site
USER 65534:65534
EXPOSE %d
ENTRYPOINT %s
`, p.image(), mounts, agent.StaticPort, quoted)
}

// buildStatic builds the static site cloned at dir into image.
func buildStatic(ctx context.Context, spec agent.AppSpec, dir, image string, log io.Writer) error {
	p := planStatic(dir, *spec.Static, spec.Env)
	describe(p, log)
	work, err := os.MkdirTemp("", "walroos-static-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	exe, err := ServerBinary()
	if err != nil {
		return err
	}
	if err := copyFile(exe, filepath.Join(work, "walroos")); err != nil {
		return fmt.Errorf("static server: %w", err)
	}
	files := map[string]string{"build.sh": p.script(), "Dockerfile": p.dockerfile(), "env": buildEnv(spec.Env)}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(text), 0o600); err != nil {
			return err
		}
	}
	return podmanBuild(ctx, spec.Name, log, "-t", image, "-f", filepath.Join(work, "Dockerfile"),
		"--build-context", "walroos="+work, "--secret", "id=env,src="+filepath.Join(work, "env"), dir)
}

func describe(p staticPlan, log io.Writer) {
	if p.runtime == "" {
		fmt.Fprintln(log, "static site: plain files, no build")
		return
	}
	fmt.Fprintf(log, "static site: %s %s (%s) with %s\n", p.runtime, p.version, p.versionFrom, p.pm)
}

// buildEnv is the app's env vars as a sh script, so the build sees them (VITE_*, NEXT_PUBLIC_*).
func buildEnv(env []string) string {
	var b strings.Builder
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && names.EnvKey(k) {
			fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
		}
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
