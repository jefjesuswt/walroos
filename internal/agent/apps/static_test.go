package apps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent"
)

func TestPlanStatic(t *testing.T) {
	vite := `{"scripts": {"build": "vite build"}}`
	cases := []struct {
		name           string
		files          map[string]string
		b              agent.StaticBuild
		runtime, pm    string
		install, build string
		servesRepo     bool
	}{
		{"plain html", map[string]string{"index.html": "hi"}, agent.StaticBuild{}, "", "", "", "", true},
		{"npm", map[string]string{"package.json": vite, "package-lock.json": "{}"}, agent.StaticBuild{}, "node", "npm", "npm ci", "npm run build", false},
		{"npm without a lockfile", map[string]string{"package.json": vite}, agent.StaticBuild{}, "node", "npm", "npm install", "npm run build", false},
		{"pnpm", map[string]string{"package.json": vite, "pnpm-lock.yaml": ""}, agent.StaticBuild{}, "node", "pnpm", "pnpm install --frozen-lockfile", "pnpm run build", false},
		{"yarn", map[string]string{"package.json": vite, "yarn.lock": ""}, agent.StaticBuild{}, "node", "yarn", "yarn install --frozen-lockfile", "yarn run build", false},
		{"bun", map[string]string{"package.json": vite, "bun.lock": ""}, agent.StaticBuild{}, "bun", "bun", "bun install --frozen-lockfile", "bun run build", false},
		{"bun asked for", map[string]string{"package.json": vite, "package-lock.json": "{}"}, agent.StaticBuild{Runtime: "bun"}, "bun", "bun", "bun install", "bun run build", false},
		{"no build script", map[string]string{"package.json": `{"scripts": {"start": "x"}}`}, agent.StaticBuild{}, "node", "npm", "npm install", "", true},
		{"commands set", map[string]string{"package.json": vite}, agent.StaticBuild{Install: "npm i --legacy-peer-deps", Build: "npm run build:prod"}, "node", "npm", "npm i --legacy-peer-deps", "npm run build:prod", false},
		{"a build with no package.json", map[string]string{"gen.sh": ""}, agent.StaticBuild{Build: "sh gen.sh"}, "node", "npm", "", "sh gen.sh", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for f, body := range c.files {
			os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
		}
		p := planStatic(dir, c.b, nil)
		servesRepo := p.candidates[len(p.candidates)-1] == "."
		if p.runtime != c.runtime || p.pm != c.pm || p.install != c.install || p.build != c.build || servesRepo != c.servesRepo {
			t.Errorf("%s: got %+v", c.name, p)
		}
	}
}

// TestStaticVersions: like Netlify, NODE_VERSION and BUN_VERSION pick the runtime's version (and
// BUN_VERSION Bun itself), then the repo's .nvmrc or .node-version; anything else is skipped.
func TestStaticVersions(t *testing.T) {
	pkg := map[string]string{"package.json": `{"scripts": {"build": "x"}}`}
	cases := []struct {
		name          string
		files         map[string]string
		env           []string
		image, source string
	}{
		{"default", nil, nil, "docker.io/library/node:24-slim", "default"},
		{"NODE_VERSION", map[string]string{".nvmrc": "18"}, []string{"NODE_VERSION=20.11"}, "docker.io/library/node:20.11-slim", "NODE_VERSION"},
		{".nvmrc", map[string]string{".nvmrc": "v18.19.0\n"}, nil, "docker.io/library/node:18.19.0-slim", ".nvmrc"},
		{".nvmrc lts", map[string]string{".nvmrc": "lts/iron"}, nil, "docker.io/library/node:lts-slim", ".nvmrc"},
		{".node-version", map[string]string{".node-version": "22"}, nil, "docker.io/library/node:22-slim", ".node-version"},
		{"not a version", nil, []string{"NODE_VERSION=20; rm -rf /"}, "docker.io/library/node:24-slim", "default"},
		{"BUN_VERSION picks bun", nil, []string{"BUN_VERSION=1.2"}, "docker.io/oven/bun:1.2-slim", "BUN_VERSION"},
		{"latest", nil, []string{"BUN_VERSION=latest"}, "docker.io/oven/bun:slim", "BUN_VERSION"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for f, body := range pkg {
			os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
		}
		for f, body := range c.files {
			os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644)
		}
		p := planStatic(dir, agent.StaticBuild{}, c.env)
		if p.image() != c.image || p.versionFrom != c.source {
			t.Errorf("%s: image %s from %s", c.name, p.image(), p.versionFrom)
		}
	}
}

// TestStaticScript runs the build script with sh on a fake built repo: it reads the env file,
// finds the output folder and copies it without .git.
func TestStaticScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir, out := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "dist", "app", "browser"), 0o755)
	env := filepath.Join(t.TempDir(), "env")
	os.WriteFile(env, []byte(buildEnv([]string{"GREETING=it's \"$HOME\"", "bad key=x", "EMPTY="})), 0o600)
	p := staticPlan{runtime: "node", pm: "npm", build: `printf '%s' "$GREETING" > dist/app/browser/index.html`,
		candidates: []string{"dist/*/browser", "dist"}}
	script := strings.NewReplacer("/run/secrets/env", env, "/out", out).Replace(p.script())
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "index.html")); string(b) != `it's "$HOME"` {
		t.Fatalf("index.html = %q", b)
	}

	p.build, p.publish = "true", "public"
	cmd = exec.Command("sh", "-c", strings.NewReplacer("/run/secrets/env", env, "/out", out).Replace(p.script()))
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(b), "no folder public") {
		t.Fatalf("a missing publish folder: %v %s", err, b)
	}
}

func TestStaticDockerfile(t *testing.T) {
	d := staticPlan{runtime: "bun", pm: "bun", version: "1.1", spa: true}.dockerfile()
	for _, want := range []string{"FROM docker.io/oven/bun:1.1-slim AS build", "--mount=type=secret,id=env --mount=type=cache,target=/root/.bun/install/cache",
		"FROM scratch", `ENTRYPOINT ["/walroos","static-server","--dir","/site","--port","8080","--spa"]`} {
		if !strings.Contains(d, want) {
			t.Errorf("dockerfile lacks %q:\n%s", want, d)
		}
	}
}
