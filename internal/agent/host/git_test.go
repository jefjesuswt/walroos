package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent/agenttest"
)

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
		if got := HTTPSURL(in); got != want {
			t.Errorf("HTTPSURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitClone(t *testing.T) {
	ctx := context.Background()

	// A plain URL clones as given: no SSH, no fallback.
	src := agenttest.GitRepo(t, map[string]string{"README.md": "x\n"})
	dst := filepath.Join(t.TempDir(), "plain")
	if used, err := GitClone(ctx, []string{"clone"}, src, dst, ""); err != nil || used != src {
		t.Fatalf("local clone: used %q, %v", used, err)
	}

	// Both ways fail: the error says both, hints at the node's key, and leaves dir empty.
	dir := t.TempDir()
	_, err := GitClone(ctx, []string{"clone", "--depth", "1"}, "git@nonexistent.invalid:owner/repo.git", dir, "")
	if err == nil || !strings.Contains(err.Error(), "over SSH") || !strings.Contains(err.Error(), "over https") || !strings.Contains(err.Error(), "GitHub token in Settings") {
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
	orig := GitSSH
	GitSSH += " -F /dev/null -o IdentitiesOnly=yes -o IdentityFile=/nonexistent"
	t.Cleanup(func() { GitSSH = orig })
	t.Setenv("SSH_AUTH_SOCK", "")
	used, err := GitClone(ctx, []string{"clone", "--depth", "1"}, "git@github.com:octocat/Hello-World.git", filepath.Join(t.TempDir(), "pub"), "")
	if err != nil || used != "https://github.com/octocat/Hello-World.git" {
		t.Fatalf("public repo, no keys: used %q, %v", used, err)
	}
}

// The token reaches git as an auth header for github.com only, and never through the URL.
func TestTokenEnv(t *testing.T) {
	env := strings.Join(tokenEnv("ghp_x"), "\n")
	if !strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader") || !strings.Contains(env, "Basic eC1hY2Nlc3MtdG9rZW46Z2hwX3g=") {
		t.Fatalf("env:\n%s", env)
	}
	for u, want := range map[string]bool{"https://github.com/me/app.git": true, "https://gitlab.com/me/app": false, "https://github.com.evil.test/x": false, "/srv/repo": false} {
		if isGitHub(u) != want {
			t.Errorf("isGitHub(%q) = %v", u, !want)
		}
	}
	// A token with a repo that isn't on github.com is ignored: the local clone still works.
	src := agenttest.GitRepo(t, map[string]string{"README.md": "x\n"})
	if used, err := GitClone(context.Background(), []string{"clone"}, src, filepath.Join(t.TempDir(), "c"), "ghp_x"); err != nil || used != src {
		t.Fatalf("used %q, %v", used, err)
	}
}
