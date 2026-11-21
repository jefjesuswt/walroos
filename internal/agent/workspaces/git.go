package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

// initRepo makes dir (an empty volume) a git repo on main with files staged, not committed: the
// first commit is the user's, with their own name and email.
func initRepo(ctx context.Context, dir string, files map[string]string) error {
	if err := host.Run(ctx, nil, "git", "init", "-q", "-b", "main", dir); err != nil {
		return err
	}
	for p, body := range files {
		f := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return host.Run(ctx, nil, "git", "-C", dir, "add", "-A")
}

// gitRemote reads origin's URL from a clone without failing the rebuild if git is unhappy.
func gitRemote(ctx context.Context, repo string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeEnvFile writes KEY=VALUE lines to a 0600 temp file for podman run --env-file.
func writeEnvFile(env []string) (string, error) {
	f, err := os.CreateTemp("", "walroos-env-")
	if err != nil {
		return "", err
	}
	defer f.Close()
	os.Chmod(f.Name(), 0o600)
	for _, kv := range env {
		if _, _, ok := strings.Cut(kv, "="); !ok {
			continue
		}
		if _, err := fmt.Fprintln(f, kv); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

// specPath is where create remembers what it was asked for; rebuild reads it back.
// It lives in the home volume, so it survives a rebuild and never shows up in the repo.
const specPath = ".walroos/workspace.json"

func saveSpec(ctx context.Context, homeVolume string, spec agent.WorkspaceSpec) {
	home, err := host.Podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", homeVolume)
	if err != nil {
		return // best effort: rebuild falls back to the git remote
	}
	if err := os.MkdirAll(filepath.Join(home, filepath.Dir(specPath)), 0o700); err != nil {
		return
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(home, specPath), body, 0o600)
}

func loadSpec(ctx context.Context, homeVolume string) agent.WorkspaceSpec {
	var spec agent.WorkspaceSpec
	home, err := host.Podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", homeVolume)
	if err != nil {
		return spec
	}
	body, err := os.ReadFile(filepath.Join(home, specPath))
	if err == nil {
		json.Unmarshal(body, &spec)
	}
	return spec
}

// repoName is the folder name for a repo URL: github.com/me/app.git → app.
func repoName(url string) string {
	n := strings.TrimSuffix(path.Base(strings.TrimRight(strings.ReplaceAll(url, ":", "/"), "/")), ".git")
	if !folderRe.MatchString(n) || n == "." || n == ".." {
		return "src"
	}
	return n
}

var folderRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
