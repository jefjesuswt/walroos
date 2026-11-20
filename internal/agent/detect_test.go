package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectDir(t *testing.T) {
	droidGradle := `android {
    compileSdk = 37
    buildToolsVersion = "36.0.0"
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17 }
}`
	cases := []struct {
		name  string
		files map[string]string
		stack string
		image string
		user  string
		tools string // ids, "+" = postCreate
		post  string // substring of postCreateCommand
	}{
		{"droid android", map[string]string{"build.gradle.kts": "plugins {}", "settings.gradle.kts": "", "app/build.gradle.kts": droidGradle,
			"app/src/main/AndroidManifest.xml": "<manifest/>"},
			"android", "mcr.microsoft.com/devcontainers/java:1-17-bookworm", "vscode", "jdk +android-sdk", `"platforms;android-37.0" "build-tools;36.0.0"`},
		{"older android sdk", map[string]string{"build.gradle": "", "app/build.gradle": "android { compileSdkVersion 34 }",
			"app/src/main/AndroidManifest.xml": ""},
			"android", "mcr.microsoft.com/devcontainers/java:1-17-bookworm", "vscode", "jdk +android-sdk", `"platforms;android-34" "build-tools;34.0.0"`},
		{"dotnet global.json", map[string]string{"global.json": `{"sdk": {"version": "8.0.404"}}`, "src/Api/Api.csproj": "<TargetFramework>net9.0</TargetFramework>"},
			"dotnet", "mcr.microsoft.com/devcontainers/dotnet:8.0", "vscode", "dotnet", ""},
		{"dotnet csproj", map[string]string{"App.slnx": "", "src/Api/Api.csproj": "<TargetFramework>net9.0</TargetFramework>"},
			"dotnet", "mcr.microsoft.com/devcontainers/dotnet:9.0", "vscode", "dotnet", ""},
		{"bun", map[string]string{"package.json": "{}", "bun.lock": "", ".nvmrc": "v20.11.0\n"},
			"bun", "mcr.microsoft.com/devcontainers/javascript-node:20", "node", "node +bun +deps", "npm install -g bun && bun install"},
		{"pnpm", map[string]string{"package.json": "{}", "pnpm-lock.yaml": ""},
			"node", "mcr.microsoft.com/devcontainers/javascript-node:22", "node", "node +pnpm +deps", "pnpm install"},
		{"yarn", map[string]string{"package.json": "{}", "yarn.lock": ""}, "node", "mcr.microsoft.com/devcontainers/javascript-node:22", "node", "node yarn +deps", "yarn install"},
		{"go root wins over web/", map[string]string{"go.mod": "module x\n\ngo 1.27.4\n", "web/package.json": "{}", "web/bun.lock": ""},
			"go", "mcr.microsoft.com/devcontainers/go:1.27", "vscode", "go", ""},
		{"js only in a subfolder", map[string]string{"README.md": "", "web/package.json": "{}", "web/bun.lock": ""},
			"bun", "mcr.microsoft.com/devcontainers/javascript-node:22", "node", "node +bun +deps", "cd web && bun install"},
		{"rust", map[string]string{"Cargo.toml": ""}, "rust", "mcr.microsoft.com/devcontainers/rust:1", "vscode", "rust", ""},
		{"python uv", map[string]string{"pyproject.toml": `requires-python = ">=3.12"`, "uv.lock": ""},
			"python", "mcr.microsoft.com/devcontainers/python:3.12", "vscode", "python +uv +deps", "pipx install uv"},
		{"python requirements", map[string]string{"requirements.txt": "", ".python-version": "3.11.4"},
			"python", "mcr.microsoft.com/devcontainers/python:3.11", "vscode", "python", ""},
		{"java gradle", map[string]string{"build.gradle.kts": "kotlin { jvmToolchain(21) }"},
			"java", "mcr.microsoft.com/devcontainers/java:1-21-bookworm", "vscode", "jdk", ""},
		{"own devcontainer", map[string]string{".devcontainer/devcontainer.json": "{}", "package.json": "{}"}, "devcontainer", "", "", "", ""},
		{"nothing", map[string]string{"README.md": ""}, "none", "", "", "mise", ""},
		{"deps folders ignored", map[string]string{"README.md": "", "node_modules/x/package.json": "{}"}, "none", "", "", "mise", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, body := range c.files {
				writeFile(t, filepath.Join(dir, p), body)
			}
			d := detectDir(dir)
			var ids []string
			for _, tool := range d.Tools {
				id := tool.ID
				if tool.Source == "postCreate" {
					id = "+" + id
				}
				ids = append(ids, id)
			}
			if d.Stack != c.stack || d.Image != c.image || d.RemoteUser != c.user || strings.Join(ids, " ") != c.tools {
				t.Fatalf("got %s %q %q [%s], want %s %q %q [%s]", d.Stack, d.Image, d.RemoteUser, strings.Join(ids, " "), c.stack, c.image, c.user, c.tools)
			}
			if c.image == "" {
				if d.Devcontainer != nil {
					t.Fatalf("devcontainer = %s, want none (repo's own or base image)", d.Devcontainer)
				}
				return
			}
			// The generated config must be one the agent accepts, with the image and postCreate.
			dc, err := readDevcontainer(dir, "", d.Devcontainer)
			if err != nil || dc.Image != c.image || dc.RemoteUser != c.user {
				t.Fatalf("generated devcontainer %s: %+v, %v", d.Devcontainer, dc, err)
			}
			if got := strings.Join(dc.PostCreateCommand, " "); !strings.Contains(got, c.post) {
				t.Errorf("postCreateCommand %q, want it to contain %q", got, c.post)
			}
		})
	}
}

func TestDetectNotes(t *testing.T) {
	dir := t.TempDir()
	for p, body := range map[string]string{"global.json": "{}", "web/package.json": "{}", "compose.yml": ""} {
		writeFile(t, filepath.Join(dir, p), body)
	}
	notes := strings.Join(detectDir(dir).Notes, "\n")
	if !strings.Contains(notes, "web/package.json") || !strings.Contains(notes, DevNetwork) {
		t.Errorf("notes = %q, want the second stack and the compose hint", notes)
	}
}

// TestDetectClone clones for real (a local repo) and reports the default branch.
func TestDetectClone(t *testing.T) {
	repo := gitRepo(t, map[string]string{"go.mod": "module x\n\ngo 1.26\n"})
	d, err := Detect(context.Background(), repo, "")
	if err != nil || d.Stack != "go" || d.Branch != "main" {
		t.Fatalf("detect = %+v, %v", d, err)
	}
	var dc map[string]any
	if json.Unmarshal(d.Devcontainer, &dc) != nil || dc["image"] != "mcr.microsoft.com/devcontainers/go:1.26" {
		t.Errorf("devcontainer = %s", d.Devcontainer)
	}
	if _, err := Detect(context.Background(), filepath.Join(t.TempDir(), "missing"), ""); !errors.Is(err, ErrDetectClone) {
		t.Errorf("missing repo: %v, want ErrDetectClone", err)
	}
	if _, err := Detect(context.Background(), repo, "no-such-branch"); !errors.Is(err, ErrDetectClone) {
		t.Errorf("missing branch: %v, want ErrDetectClone", err)
	}
}

func TestHTTPSURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:jefjesuswt/droid":     "https://github.com/jefjesuswt/droid",
		"git@github.com:jefjesuswt/droid.git": "https://github.com/jefjesuswt/droid.git",
		"ssh://git@gitlab.com/a/b.git":        "https://gitlab.com/a/b.git",
		"ssh://git@git.example.com:2222/a/b":  "https://git.example.com/a/b",
		"https://github.com/a/b":              "https://github.com/a/b",
		"https://user:tok@github.com/a/b":     "https://user:tok@github.com/a/b",
		"/tmp/local/repo":                     "/tmp/local/repo",
		"file:///tmp/x":                       "file:///tmp/x",
	} {
		if got := httpsURL(in); got != want {
			t.Errorf("httpsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A repo's go.mod -> /dev/zero (or a FIFO) must not be read: it would eat the agent's memory.
func TestDetectSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(os.DevNull, filepath.Join(dir, "go.mod")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644)
	if d := detectDir(dir); d.Stack != "node" {
		t.Errorf("stack = %q, want node (the go.mod symlink ignored)", d.Stack)
	}
}

func TestGitClone(t *testing.T) {
	ctx := context.Background()

	// A plain URL clones as given: no SSH, no fallback.
	src := gitRepo(t, map[string]string{"README.md": "x\n"})
	dst := filepath.Join(t.TempDir(), "plain")
	if used, err := gitClone(ctx, []string{"clone"}, src, dst); err != nil || used != src {
		t.Fatalf("local clone: used %q, %v", used, err)
	}

	// Both ways fail: the error says both, hints at the node's key, and leaves dir empty.
	dir := t.TempDir()
	_, err := gitClone(ctx, []string{"clone", "--depth", "1"}, "git@nonexistent.invalid:owner/repo.git", dir)
	if err == nil || !strings.Contains(err.Error(), "over SSH") || !strings.Contains(err.Error(), "over https") || !strings.Contains(err.Error(), "SSH key on the node") {
		t.Fatalf("unreachable SSH URL: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a failed clone left %d entries behind", len(entries))
	}

	// Public repo by SSH URL on a node without keys: SSH fails, https works. Needs the network.
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		return
	}
	// Like a node without keys: no config, no identities, no agent. (ssh ignores $HOME.)
	orig := gitSSH
	gitSSH += " -F /dev/null -o IdentitiesOnly=yes -o IdentityFile=/nonexistent"
	t.Cleanup(func() { gitSSH = orig })
	t.Setenv("SSH_AUTH_SOCK", "")
	used, err := gitClone(ctx, []string{"clone", "--depth", "1"}, "git@github.com:octocat/Hello-World.git", filepath.Join(t.TempDir(), "pub"))
	if err != nil || used != "https://github.com/octocat/Hello-World.git" {
		t.Fatalf("public repo, no keys: used %q, %v", used, err)
	}
}
