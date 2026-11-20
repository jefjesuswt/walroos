package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var ErrDetectClone = errors.New("can't clone the repo")

// Tool is one thing a generated devcontainer provides. Source says where it comes from: "image"
// (already in it, can't be turned off) or "postCreate" (Install runs after the container starts).
type Tool struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source"`
	Install string `json:"install,omitempty"` // shell for postCreateCommand; the UI joins the enabled ones with &&
}

// Detection is what detect proposes for a repo. Devcontainer is the generated config with every
// tool on; it's nil when the repo has its own (used as is) or no stack matched (base image).
type Detection struct {
	Stack        string          `json:"stack"` // android, dotnet, bun, node, go, rust, python, java, devcontainer, none
	Branch       string          `json:"branch"`
	Image        string          `json:"image,omitempty"`
	RemoteUser   string          `json:"remote_user,omitempty"`
	Tools        []Tool          `json:"tools"`
	Devcontainer json.RawMessage `json:"devcontainer,omitempty"`
	Notes        []string        `json:"notes,omitempty"`
}

// Detect shallow-clones the repo into a temp dir, scans it and removes the clone.
func Detect(ctx context.Context, repoURL, branch string) (Detection, error) {
	if repoURL == "" {
		return Detection{}, fmt.Errorf("%w: repo_url is required", ErrDetectClone)
	}
	dir, err := os.MkdirTemp("", "walroos-detect-")
	if err != nil {
		return Detection{}, err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second) // the hub gives up on agents at 30s
	defer cancel()
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	if _, err := gitClone(ctx, args, repoURL, dir); err != nil {
		return Detection{}, fmt.Errorf("%w: %v", ErrDetectClone, err)
	}
	d := detectDir(dir)
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		d.Branch = strings.TrimSpace(string(out))
	}
	return d, nil
}

// skipDirs are never scanned: dependencies and build output say nothing about the stack.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "build": true, "dist": true, "bin": true, "obj": true,
	"target": true, "vendor": true, ".gradle": true, ".venv": true, "venv": true}

// scan lists dir's files (slash paths) up to 3 levels deep, shallowest first.
type scan struct {
	dir   string
	paths []string
}

// find returns the first path whose base name is one of names; root limits it to top-level files.
func (s scan) find(root bool, names ...string) string {
	for _, p := range s.paths {
		if root && strings.Contains(p, "/") {
			continue
		}
		for _, n := range names {
			if path.Base(p) == n || (strings.HasPrefix(n, "*") && strings.HasSuffix(p, n[1:])) {
				return p
			}
		}
	}
	return ""
}

func (s scan) read(rel string) string {
	if rel == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(s.dir, rel))
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20)) // manifests are small; a huge one is cut, not loaded
	return string(b)
}

// grep returns the first submatch of re in the files matching names.
func (s scan) grep(re string, names ...string) string {
	for _, p := range s.paths {
		for _, n := range names {
			if path.Base(p) == n || (strings.HasPrefix(n, "*") && strings.HasSuffix(p, n[1:])) {
				if v := match(s.read(p), re); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// detectDir picks the stack from the files in dir and generates a devcontainer. Markers at the
// root win (walroos: go.mod at the root, a bun app in web/); deeper ones are the fallback.
// ponytail: one stack per repo; a second one only becomes a note.
func detectDir(dir string) Detection {
	s := scan{dir: dir}
	var deeper []string
	fs.WalkDir(os.DirFS(dir), ".", func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
		case e.IsDir() && p != "." && (skipDirs[e.Name()] || strings.Count(p, "/") >= 3):
			return fs.SkipDir
		case e.IsDir():
		case !e.Type().IsRegular(): // a symlink to /dev/zero or a FIFO would eat memory or hang
		case strings.Contains(p, "/"):
			deeper = append(deeper, p)
		default:
			s.paths = append(s.paths, p)
		}
		return nil
	})
	slices.SortStableFunc(deeper, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	s.paths = append(s.paths, deeper...)

	d, g := pick(s, true)
	if d.Stack == "none" {
		d, g = pick(s, false)
	}
	if p := s.find(false, "package.json"); p != "" && d.Stack != "node" && d.Stack != "bun" && d.Stack != "devcontainer" {
		d.Notes = append(d.Notes, "Also found "+p+": add Node.js to the devcontainer (or `mise use node` on the base image).")
	}
	if p := s.find(false, "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"); p != "" {
		d.Notes = append(d.Notes, p+": run those services on the node and join them to the "+DevNetwork+" network; the workspace reaches them by name.")
	}
	if g.image != "" {
		d.Image, d.RemoteUser = g.image, g.user
		d.Devcontainer = g.json(d.Tools)
	}
	return d
}

// pick matches stack markers, at the root only or anywhere.
func pick(s scan, root bool) (Detection, gen) {
	has := func(names ...string) string { return s.find(root, names...) }
	jdkRe := `(?:VERSION_|JVM_|jvmToolchain\(|<maven\.compiler\.release>|<release>)(\d+)` // VERSION_17, JVM_17, jvmToolchain(17)

	var d Detection
	g := gen{}
	switch {
	case s.find(true, ".devcontainer.json") != "" || s.find(false, "devcontainer.json") == ".devcontainer/devcontainer.json":
		d.Stack = "devcontainer"
		d.Notes = append(d.Notes, "The repo has its own devcontainer.json; it's used as is.")

	case s.find(false, "AndroidManifest.xml") != "" && has("build.gradle.kts", "build.gradle", "settings.gradle.kts", "settings.gradle") != "":
		gradles := []string{"build.gradle.kts", "build.gradle"} // the app module's has compileSdk, the root one doesn't
		jdk := cmpOr(s.grep(jdkRe, gradles...), "17")
		sdk := cmpOr(s.grep(`compileSdk(?:Version)?\s*=?\s*(\d+)`, gradles...), "35")
		buildTools := cmpOr(s.grep(`buildToolsVersion\s*=?\s*"([\d.]+)"`, gradles...), sdk+".0.0")
		platform := "android-" + sdk
		if n, _ := strconv.Atoi(sdk); n >= 37 { // newer SDKs have minor versions: platforms;android-37.0
			platform += ".0"
		}
		d.Stack, g.image, g.user = "android", "mcr.microsoft.com/devcontainers/java:1-"+jdk+"-bookworm", "vscode"
		g.env = map[string]string{"ANDROID_HOME": "/home/vscode/android-sdk", "ANDROID_SDK_ROOT": "/home/vscode/android-sdk"}
		d.Tools = append(d.Tools, Tool{ID: "jdk", Label: "JDK", Version: jdk, Source: "image"},
			Tool{ID: "android-sdk", Label: "Android SDK", Version: sdk, Source: "postCreate", Install: androidSDK(platform, buildTools)})
		d.Notes = append(d.Notes, fmt.Sprintf("compileSdk %s · build-tools %s", sdk, buildTools),
			`If Gradle fails with "Temporary failure in name resolution", set JAVA_TOOL_OPTIONS=-Djava.net.preferIPv4Stack=true.`)

	case has("global.json", "*.csproj", "*.sln", "*.slnx") != "":
		v := match(s.read(has("global.json")), `"version"\s*:\s*"(\d+\.\d+)`)
		v = cmpOr(cmpOr(v, s.grep(`<TargetFrameworks?>net(\d+\.\d+)`, "*.csproj")), "10.0")
		d.Stack, g.image, g.user = "dotnet", "mcr.microsoft.com/devcontainers/dotnet:"+v, "vscode"
		d.Tools = append(d.Tools, Tool{ID: "dotnet", Label: ".NET SDK", Version: v, Source: "image"})

	case has("bun.lock", "bun.lockb", "package.json") != "":
		node := cmpOr(match(s.read(has(".nvmrc", ".node-version")), `v?(\d+)`), "22")
		d.Stack, g.image, g.user = "node", "mcr.microsoft.com/devcontainers/javascript-node:"+node, "node"
		d.Tools = append(d.Tools, Tool{ID: "node", Label: "Node.js", Version: node, Source: "image"})
		deps := Tool{ID: "deps", Label: "Install dependencies", Source: "postCreate", Install: "npm install"}
		switch {
		case has("bun.lock", "bun.lockb") != "":
			d.Stack, deps.Install = "bun", "bun install"
			d.Tools = append(d.Tools, Tool{ID: "bun", Label: "Bun", Source: "postCreate", Install: "npm install -g bun"})
		case has("pnpm-lock.yaml") != "":
			deps.Install = "pnpm install"
			d.Tools = append(d.Tools, Tool{ID: "pnpm", Label: "pnpm", Source: "postCreate", Install: "npm install -g pnpm"})
		case has("yarn.lock") != "":
			deps.Install = "yarn install"
			d.Tools = append(d.Tools, Tool{ID: "yarn", Label: "Yarn", Source: "image"})
		}
		if sub := path.Dir(has("bun.lock", "bun.lockb", "package.json")); sub != "." { // install where the lockfile is
			deps.Install = "cd " + sub + " && " + deps.Install
		}
		d.Tools = append(d.Tools, deps)

	case has("go.mod") != "":
		v := cmpOr(match(s.read(has("go.mod")), `(?m)^go (\d+\.\d+)`), "1")
		d.Stack, g.image, g.user = "go", "mcr.microsoft.com/devcontainers/go:"+v, "vscode"
		d.Tools = append(d.Tools, Tool{ID: "go", Label: "Go", Version: v, Source: "image"})

	case has("Cargo.toml") != "":
		d.Stack, g.image, g.user = "rust", "mcr.microsoft.com/devcontainers/rust:1", "vscode"
		d.Tools = append(d.Tools, Tool{ID: "rust", Label: "Rust", Source: "image"})

	case has("pyproject.toml", "requirements.txt") != "":
		v := match(s.read(has(".python-version")), `(\d+\.\d+)`)
		v = cmpOr(cmpOr(v, match(s.read(has("pyproject.toml")), `requires-python\s*=\s*"[>=~^]*(\d+\.\d+)`)), "3.13")
		d.Stack, g.image, g.user = "python", "mcr.microsoft.com/devcontainers/python:"+v, "vscode"
		d.Tools = append(d.Tools, Tool{ID: "python", Label: "Python", Version: v, Source: "image"})
		if has("uv.lock") != "" {
			d.Tools = append(d.Tools, Tool{ID: "uv", Label: "uv", Source: "postCreate", Install: "pipx install uv"},
				Tool{ID: "deps", Label: "Install dependencies", Source: "postCreate", Install: "~/.local/bin/uv sync"})
		}

	case has("build.gradle.kts", "build.gradle", "pom.xml") != "":
		jdk := cmpOr(match(s.read(has("build.gradle.kts", "build.gradle", "pom.xml")), jdkRe), "17")
		d.Stack, g.image, g.user = "java", "mcr.microsoft.com/devcontainers/java:1-"+jdk+"-bookworm", "vscode"
		d.Tools = append(d.Tools, Tool{ID: "jdk", Label: "JDK", Version: jdk, Source: "image"})

	default:
		d.Stack = "none"
		d.Tools = append(d.Tools, Tool{ID: "mise", Label: "mise", Source: "image"})
		d.Notes = append(d.Notes, "No stack detected: the walroos base image has mise, install runtimes with `mise use`.")
	}
	return d, g
}

// gen is a generated devcontainer.json, before tools add their postCreate.
type gen struct {
	image, user string
	env         map[string]string
}

func (g gen) json(tools []Tool) json.RawMessage {
	var cmds []string
	for _, t := range tools {
		if t.Install != "" {
			cmds = append(cmds, t.Install)
		}
	}
	b, _ := json.MarshalIndent(struct {
		Image             string            `json:"image"`
		RemoteUser        string            `json:"remoteUser,omitempty"`
		ContainerEnv      map[string]string `json:"containerEnv,omitempty"`
		PostCreateCommand string            `json:"postCreateCommand,omitempty"`
	}{g.image, g.user, g.env, strings.Join(cmds, " && ")}, "", "  ")
	return b
}

// androidSDK installs cmdline-tools (once: it lives in $HOME, which survives rebuilds) and the packages.
// ponytail: cmdline-tools build pinned; bump it when sdkmanager asks for a newer one.
func androidSDK(platform, buildTools string) string {
	const clt = "https://dl.google.com/android/repository/commandlinetools-linux-15859902_latest.zip"
	sdkm := `"$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"`
	return `{ test -x ` + sdkm + ` || { mkdir -p "$ANDROID_HOME/cmdline-tools" && curl -fsSLo /tmp/clt.zip ` + clt +
		` && unzip -q /tmp/clt.zip -d "$ANDROID_HOME/cmdline-tools" && mv "$ANDROID_HOME/cmdline-tools/cmdline-tools" "$ANDROID_HOME/cmdline-tools/latest" && rm /tmp/clt.zip; }; }` +
		` && { yes | ` + sdkm + ` --licenses >/dev/null || true; }` +
		` && ` + sdkm + ` "platform-tools" "platforms;` + platform + `" "build-tools;` + buildTools + `"`
}

func match(s, re string) string {
	if m := regexp.MustCompile(re).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
