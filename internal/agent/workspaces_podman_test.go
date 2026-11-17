package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkspaceLifecycle drives a real rootless podman: clone a local repo whose devcontainer
// builds an image with a non-root user, then start/stop/delete. Opt-in, it pulls alpine:
//
//	WALROOS_PODMAN_TEST=1 go test ./internal/agent -run Lifecycle -v
func TestWorkspaceLifecycle(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
	ctx := context.Background()

	// A git repo with a devcontainer: custom Dockerfile, non-root remoteUser, env, postCreate.
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	writeFile(t, filepath.Join(repo, ".devcontainer/Dockerfile"), "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1001 dev\n")
	writeFile(t, filepath.Join(repo, ".devcontainer/devcontainer.json"), `{
		"build": {"dockerfile": "Dockerfile"},
		"remoteUser": "dev",
		"containerEnv": {"WALROOS_TEST": "yes"},
		"postCreateCommand": "echo $WALROOS_TEST > ~/post-created",
	}`)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	b := make([]byte, 4)
	rand.Read(b)
	name := "walroos-test-" + hex.EncodeToString(b)
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo, Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}); !errors.Is(err, ErrWorkspaceExists) {
		t.Fatalf("second create: %v, want ErrWorkspaceExists", err)
	}

	ws := waitState(t, w, name, "running")
	if ws.User != "1001:1001" || ws.Folder != "/workspaces/"+filepath.Base(repo) || ws.Image != "localhost/walroos-ws-"+name+":latest" {
		t.Fatalf("workspace = %+v", ws)
	}

	sh := func(script string) string {
		t.Helper()
		out, err := podman(ctx, "exec", "--user", ws.User, "-w", ws.Folder, "ws-"+name, "/bin/sh", "-c", script)
		if err != nil {
			t.Fatalf("exec %q: %v", script, err)
		}
		return out
	}
	if got := sh("stat -c %U README.md"); got != "dev" {
		t.Errorf("cloned files owned by %q, want dev (keep-id mapping)", got)
	}
	if got := sh("cat ~/post-created"); got != "yes" {
		t.Errorf("postCreateCommand output %q, want yes (containerEnv + $HOME volume)", got)
	}
	if got := sh("touch new-file && stat -c %U new-file"); got != "dev" {
		t.Errorf("new files owned by %q, want dev", got)
	}

	if err := w.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "stopped")
	if err := w.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running")
	if got := sh("cat ~/post-created; ls new-file"); got != "yes\nnew-file" {
		t.Errorf("after restart: %q, want home and repo files to survive", got)
	}

	if err := w.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Get(ctx, name); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("after delete: %v, want ErrWorkspaceNotFound", err)
	}
	if out, _ := podman(ctx, "volume", "ls", "-q", "--filter", "name=ws-"+name); out != "" {
		t.Errorf("volumes left after delete: %s", out)
	}
}

// TestWorkspaceBaseImage: a repo without devcontainer.json runs on BaseImage as root.
func TestWorkspaceBaseImage(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "walroos-test-" + hex.EncodeToString(b)
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })

	if _, err := w.Create(context.Background(), WorkspaceSpec{Name: name, RepoURL: repo}); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	if ws.User != "0:0" || ws.Image != BaseImage {
		t.Fatalf("workspace = %+v, want root on %s", ws, BaseImage)
	}
	out, err := podman(context.Background(), "exec", "-w", ws.Folder, "ws-"+name, "/bin/sh", "-c", "ls; echo $HOME")
	if err != nil || out != "main.go\n/root" {
		t.Fatalf("exec: %q, %v", out, err)
	}
}

// TestWorkspaceImageMetadataUser: the image's devcontainer.metadata remoteUser is used, and
// devcontainer.json's remoteUser overrides it.
func TestWorkspaceImageMetadataUser(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
	dockerfile := "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1001 meta && adduser -D -u 1002 override\n" +
		`LABEL devcontainer.metadata='[{"id":"some-feature"},{"remoteUser":"meta"}]'` + "\n"
	for _, c := range []struct{ json, want string }{
		{`{"build": {"dockerfile": "Dockerfile"}}`, "1001:1001"},
		{`{"build": {"dockerfile": "Dockerfile"}, "remoteUser": "override"}`, "1002:1002"},
	} {
		repo := t.TempDir()
		writeFile(t, filepath.Join(repo, ".devcontainer/Dockerfile"), dockerfile)
		writeFile(t, filepath.Join(repo, ".devcontainer/devcontainer.json"), c.json)
		for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
			if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		b := make([]byte, 4)
		rand.Read(b)
		name := "walroos-test-" + hex.EncodeToString(b)
		w := NewWorkspaces()
		t.Cleanup(func() { w.Delete(context.Background(), name) })
		if _, err := w.Create(context.Background(), WorkspaceSpec{Name: name, RepoURL: repo}); err != nil {
			t.Fatal(err)
		}
		if ws := waitState(t, w, name, "running"); ws.User != c.want {
			t.Errorf("%s: user %s, want %s", c.json, ws.User, c.want)
		}
	}
}

// TestWorkspaceCreateFails: a clone error surfaces as state failed with git's message, and Delete cleans up.
func TestWorkspaceCreateFails(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "walroos-test-" + hex.EncodeToString(b)
	w := NewWorkspaces()
	ctx := context.Background()

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: filepath.Join(t.TempDir(), "nope")}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		ws, err := w.Get(ctx, name)
		if err == nil && ws.State == "failed" {
			if ws.Error == "" {
				t.Fatal("failed without an error message")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never failed: %+v, %v", ws, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := w.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Get(ctx, name); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if out, _ := podman(ctx, "volume", "ls", "-q", "--filter", "name=ws-"+name); out != "" {
		t.Errorf("volumes left after delete: %s", out)
	}
}

func waitState(t *testing.T, w *Workspaces, name, want string) Workspace {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		ws, err := w.Get(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if ws.State == want {
			return ws
		}
		// Only creating may come before want: a transient "stopped" right after creation was a real bug.
		if ws.State != "creating" || time.Now().After(deadline) {
			t.Fatalf("waiting for %s: %+v", want, ws)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
