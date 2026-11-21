// Package agenttest has the helpers the agent's packages share in their tests.
package agenttest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// NeedPodman skips the test unless WALROOS_PODMAN_TEST is set: it runs real containers.
func NeedPodman(t *testing.T) {
	t.Helper()
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
}

// WriteFile writes body to path, making its directories.
func WriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// GitRepo makes a git repo with files committed on main.
func GitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for path, body := range files {
		WriteFile(t, filepath.Join(repo, path), body)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

// WaitState polls a workspace's state until it's want. Only "creating" may come before it: a
// transient "stopped" right after creation was a real bug.
func WaitState(t *testing.T, state func() (string, error), want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s, err := state()
		if err != nil {
			t.Fatal(err)
		}
		if s == want {
			return
		}
		if s != "creating" || time.Now().After(deadline) {
			t.Fatalf("waiting for %s: state %s", want, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Name is a fresh name for a test's workspace, app or database: walroos-test-<8 hex>.
func Name() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "walroos-test-" + hex.EncodeToString(b)
}
