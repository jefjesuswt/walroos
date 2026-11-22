package host

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	env := strings.Join(tokenEnv("https://github.com/", "tok-x"), "\n")
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:tok-x"))
	if !strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader") || !strings.Contains(env, header) {
		t.Fatalf("env:\n%s", env)
	}
	for u, want := range map[string]bool{"https://github.com/me/app.git": true, "https://gitlab.com/me/app": false, "https://github.com.evil.test/x": false, "/srv/repo": false} {
		if isGitHub(u) != want {
			t.Errorf("isGitHub(%q) = %v", u, !want)
		}
	}
	// A token with a repo that isn't on github.com is ignored: the local clone still works.
	src := agenttest.GitRepo(t, map[string]string{"README.md": "x\n"})
	if used, err := GitClone(context.Background(), []string{"clone"}, src, filepath.Join(t.TempDir(), "c"), "tok-x"); err != nil || used != src {
		t.Fatalf("used %q, %v", used, err)
	}
}

// TestTokenClone clones from an HTTP git server that, like GitHub for a private repo, answers
// 401 without the token: with tokenEnv's header git gets in, and the clone keeps no trace of it.
func TestTokenClone(t *testing.T) {
	src := agenttest.GitRepo(t, map[string]string{"index.html": "private\n"})
	bare := filepath.Join(t.TempDir(), "repo.git")
	for _, args := range [][]string{{"clone", "--bare", "-q", src, bare}, {"-C", bare, "update-server-info"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:tok-test"))
	files := http.StripPrefix("/me/", http.FileServer(http.Dir(filepath.Dir(bare))))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	ctx, url := context.Background(), srv.URL+"/me/repo.git"
	env := []string{"GIT_TERMINAL_PROMPT=0"}

	if err := Run(ctx, env, "git", "clone", "-q", url, filepath.Join(t.TempDir(), "no")); err == nil {
		t.Fatal("cloned a private repo without the token")
	}
	dir := filepath.Join(t.TempDir(), "yes")
	if err := Run(ctx, append(env, tokenEnv(srv.URL+"/", "tok-test")...), "git", "clone", "-q", url, dir); err != nil {
		t.Fatalf("with the token: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != "private\n" {
		t.Fatalf("index.html = %q", b)
	}
	if cfg, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); strings.Contains(string(cfg), "tok-test") || strings.Contains(string(cfg), "Authorization") {
		t.Fatalf("the token ended up in .git/config:\n%s", cfg)
	}
}

// Like GitHub: a token that's expired or revoked gets a 401 even on a public repo. Then the clone
// goes on without it, so public repos keep deploying after the token dies; a private repo says
// what the token needs.
func TestTokenFallback(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"public", "private"} {
		src := agenttest.GitRepo(t, map[string]string{"name": name + "\n"})
		bare := filepath.Join(root, name+".git")
		for _, args := range [][]string{{"clone", "--bare", "-q", src, bare}, {"-C", bare, "update-server-info"}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	good := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:tok-good"))
	files := http.StripPrefix("/me/", http.FileServer(http.Dir(root)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "" && auth != good || auth == "" && strings.HasPrefix(r.URL.Path, "/me/private.git") {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	orig := gitHub
	gitHub = srv.URL + "/"
	t.Cleanup(func() { gitHub = orig })
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	ctx := context.Background()
	clone := func(repo, token string) error {
		_, err := GitClone(ctx, []string{"clone", "-q"}, srv.URL+"/me/"+repo+".git", filepath.Join(t.TempDir(), "c"), token)
		return err
	}

	if err := clone("public", "tok-expired"); err != nil {
		t.Errorf("a public repo with a dead token: %v", err)
	}
	if err := clone("private", "tok-good"); err != nil {
		t.Errorf("a private repo with the token: %v", err)
	}
	branch := func(repo, token string) error {
		_, err := GitClone(ctx, []string{"clone", "-q", "--branch", "nope"}, srv.URL+"/me/"+repo+".git", filepath.Join(t.TempDir(), "b"), token)
		return err
	}
	for _, tok := range []string{"tok-good", ""} {
		if err := branch("public", tok); err == nil || err.Error() != `the repo has no branch "nope": check the branch name, or leave it empty for the default branch` {
			t.Errorf("a missing branch (token %q): %v", tok, err)
		}
	}
	if err := clone("private", "tok-expired"); err == nil || !strings.Contains(err.Error(), "with the hub's GitHub token") || !strings.Contains(err.Error(), "Contents: read") {
		t.Errorf("a private repo with a dead token: %v", err)
	}
}

func TestRepoFiles(t *testing.T) {
	src := agenttest.GitRepo(t, map[string]string{"docker-compose.yml": "services: {}\n", "api/.env": "A=1\n", "secret": "x"})
	os.Symlink("/etc/passwd", filepath.Join(src, "link"))
	exec.Command("git", "-C", src, "add", "-A").Run()
	exec.Command("git", "-C", src, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "link").Run()
	got, err := RepoFiles(context.Background(), src, "", "", []string{"docker-compose.yml", "api/../api/.env", "missing", "../outside", "/etc/passwd", "link", "api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["docker-compose.yml"] != "services: {}\n" || got["api/.env"] != "A=1\n" {
		t.Fatalf("files = %v", got)
	}
}
