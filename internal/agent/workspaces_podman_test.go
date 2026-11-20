package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
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

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo, Branch: "main"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); !errors.Is(err, ErrWorkspaceExists) {
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

// TestWorkspaceBaseImage: a repo without devcontainer.json runs on the walroos base image, as dev,
// with the tools the base image promises.
func TestWorkspaceBaseImage(t *testing.T) {
	needPodman(t)
	repo := gitRepo(t, map[string]string{"main.go": "package main\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })

	if _, err := w.Create(context.Background(), WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running", 10*time.Minute) // first run builds the base image
	if ws.User != "1000:1000" || ws.Image != BaseImage() {
		t.Fatalf("workspace = %+v, want dev (1000) on %s", ws, BaseImage())
	}

	// Non-interactive, like `ssh host cmd` or VS Code: no .bashrc, so tools must be on PATH.
	sh := func(script string) string {
		t.Helper()
		out, err := podman(context.Background(), "exec", "--user", ws.User, "-w", ws.Folder, "ws-"+name, "/bin/sh", "-c", script)
		if err != nil {
			t.Fatalf("exec %q: %v", script, err)
		}
		return out
	}
	checks := map[string]string{
		"whoami; echo $HOME":                                    "dev\n/home/dev",
		"stat -c %U main.go":                                    "dev",
		"git --version >/dev/null && echo ok":                   "ok",
		"curl --version >/dev/null && echo ok":                  "ok",
		"mise --version >/dev/null && echo ok":                  "ok",
		"test -x /usr/lib/openssh/sftp-server && echo ok":       "ok",
		"sudo -n true && echo ok":                               "ok",
		"echo $PATH | grep -q mise/shims && echo ok":            "ok",
		"test -f ~/.bashrc && echo ok":                          "ok", // image's home copied into the home volume
		"bash -lc 'echo $PATH' | grep -q mise/shims && echo ok": "ok", // login shell (interactive ssh) keeps the shims
		"ls /usr/lib/*/libicuuc.so.* >/dev/null && echo ok":     "ok", // ICU, .NET crashes without it
	}
	for script, want := range checks {
		if got := sh(script); got != want {
			t.Errorf("%s: got %q, want %q", script, got, want)
		}
	}
}

// TestWorkspaceBaseImageBuildFails: if the base image can't be built, the workspace ends failed
// with a readable error instead of hanging or running on something else.
func TestWorkspaceBaseImageBuildFails(t *testing.T) {
	needPodman(t)
	orig := baseContainerfile
	baseContainerfile = []byte("FROM docker.io/library/alpine:3.22\nRUN echo base-broke >&2 && false\n")
	t.Cleanup(func() { baseContainerfile = orig })

	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(context.Background(), WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "failed", time.Minute)
	if !strings.Contains(ws.Error, "build base image") || !strings.Contains(ws.Error, "base-broke") {
		t.Fatalf("error = %q, want the build failure", ws.Error)
	}
}

// TestWorkspaceConcurrentCreates: several workspaces on the base image at once all come up
// (the base image is built once, under baseMu, if missing).
func TestWorkspaceConcurrentCreates(t *testing.T) {
	needPodman(t)
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	w := NewWorkspaces()
	names := []string{testName(), testName(), testName()}
	for _, n := range names {
		t.Cleanup(func() { w.Delete(context.Background(), n) })
		if _, err := w.Create(context.Background(), WorkspaceSpec{Name: n, RepoURL: repo}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names {
		waitState(t, w, n, "running", 10*time.Minute)
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
		if _, err := w.Create(context.Background(), WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
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

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: filepath.Join(t.TempDir(), "nope")}, nil); err != nil {
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

func waitState(t *testing.T, w *Workspaces, name, want string, timeout ...time.Duration) Workspace {
	t.Helper()
	deadline := time.Now().Add(append(timeout, 5*time.Minute)[0])
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

func needPodman(t *testing.T) {
	t.Helper()
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1 to run against real podman")
	}
}

func testName() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "walroos-test-" + hex.EncodeToString(b)
}

// gitRepo makes a local git repo with files committed on branch main.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for path, body := range files {
		writeFile(t, filepath.Join(repo, path), body)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

// TestPruneOldBaseImages: base images from older Containerfiles are removed, unless a
// workspace still runs on one; the current one always stays.
func TestPruneOldBaseImages(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	build := func(tag, label string) {
		t.Helper()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "Containerfile"), "FROM docker.io/library/alpine:3.22\nLABEL walroos.test="+label+"\n")
		if _, err := podman(ctx, "build", "-q", "-t", tag, dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { podman(context.Background(), "rmi", "-f", "--ignore", tag) })
	}
	// Its own repository, so the test never prunes the real base image.
	unused, inUse, current := "localhost/walroos-base-test:unused", "localhost/walroos-base-test:inuse", "localhost/walroos-base-test:current"
	build(unused, "a")
	build(inUse, "b")
	build(current, "c")
	ctr := testName()
	if _, err := podman(ctx, "run", "-d", "--name", ctr, inUse, "sleep", "300"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { podman(context.Background(), "rm", "-f", "-t", "0", ctr) })

	pruneOldBaseImages(ctx, current)

	exists := func(img string) bool { _, err := podman(ctx, "image", "exists", img); return err == nil }
	if exists(unused) {
		t.Error("unused old base image was kept")
	}
	if !exists(inUse) {
		t.Error("old base image in use by a container was removed")
	}
	if !exists(current) {
		t.Error("current base image was removed")
	}
}

// TestWorkspaceSurvivesAgentRestart: state lives in podman, so a fresh agent (new Workspaces)
// still sees running and stopped workspaces.
func TestWorkspaceSurvivesAgentRestart(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { NewWorkspaces().Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running")

	restarted := NewWorkspaces()
	if ws, err := restarted.Get(ctx, name); err != nil || ws.State != "running" || ws.User == "" || ws.Folder == "" {
		t.Fatalf("after agent restart: %+v, %v", ws, err)
	}
	// Stopped outside walroos (podman stop, host reboot): the agent reports it as stopped.
	if _, err := podman(ctx, "stop", "-t", "0", "ws-"+name); err != nil {
		t.Fatal(err)
	}
	if ws, _ := restarted.Get(ctx, name); ws.State != "stopped" {
		t.Fatalf("after external stop: %+v", ws)
	}
	if err := restarted.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if ws, _ := restarted.Get(ctx, name); ws.State != "running" {
		t.Fatalf("after start: %+v", ws)
	}
}

// TestWorkspaceDoesNotTouchForeignContainers: a container or volume that merely shares the
// ws-<name> naming but isn't a walroos workspace must never be reused or deleted.
func TestWorkspaceDoesNotTouchForeignContainers(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	name := testName()
	foreign := "ws-" + name
	if _, err := podman(ctx, "run", "-d", "--name", foreign, "docker.io/library/alpine:3.22", "sleep", "300"); err != nil {
		t.Fatal(err)
	}
	if _, err := podman(ctx, "volume", "create", foreign+"-src"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		podman(context.Background(), "rm", "-f", "-t", "0", foreign)
		podman(context.Background(), "volume", "rm", "-f", foreign+"-src")
	})

	w := NewWorkspaces()
	if _, err := w.Get(ctx, name); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("a foreign container is not a workspace: %v", err)
	}
	if err := w.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := podman(ctx, "container", "exists", foreign); err != nil {
		t.Error("Delete removed a container that isn't a walroos workspace")
	}
	if _, err := podman(ctx, "volume", "exists", foreign+"-src"); err != nil {
		t.Error("Delete removed a volume that isn't a walroos workspace's")
	}

	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "failed")
	t.Logf("create over foreign names fails with: %s", ws.Error)
	if out, _ := podman(ctx, "exec", foreign, "ls", "/"); out == "" {
		t.Error("the foreign container was disturbed")
	}
	w.Delete(ctx, name)
	if _, err := podman(ctx, "container", "exists", foreign); err != nil {
		t.Error("Delete after a failed create removed the foreign container")
	}
}

// TestWorkspaceRetryAfterFailedClone: creating again (without deleting) after a failed clone
// reuses the volume and clones cleanly.
func TestWorkspaceRetryAfterFailedClone(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: filepath.Join(t.TempDir(), "nope")}, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "failed")

	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	ws := waitState(t, w, name, "running")
	if out, err := podman(ctx, "exec", "-w", ws.Folder, "ws-"+name, "cat", "README.md"); err != nil || out != "x" {
		t.Fatalf("after retry: %q, %v", out, err)
	}
}

// TestWorkspaceMiseToolPersists: what `mise use -g` installs lands in the $HOME volume, so it
// survives stop/start. Needs network (downloads jq from GitHub).
func TestWorkspaceMiseToolPersists(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	sh := func(script string) (string, error) {
		return podman(ctx, "exec", "--user", ws.User, "-w", ws.Folder, "ws-"+name, "/bin/bash", "-c", script)
	}
	if out, err := sh("mise use -g -y jq@1.7.1 2>&1 && jq --version"); err != nil || !strings.HasSuffix(out, "jq-1.7.1") {
		t.Fatalf("mise install: %q, %v", out, err)
	}
	if err := w.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	if out, err := sh("jq --version"); err != nil || out != "jq-1.7.1" {
		t.Fatalf("after restart (non-interactive, via shims): %q, %v", out, err)
	}
}

// TestWorkspaceRebuild: the container and the built image are recreated, while the clone and
// $HOME survive; it also picks up a devcontainer change (a different remoteUser/uid).
func TestWorkspaceRebuild(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{
		".devcontainer/Dockerfile":        "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1001 dev\n",
		".devcontainer/devcontainer.json": `{"build":{"dockerfile":"Dockerfile"},"remoteUser":"dev"}`,
		"README.md":                       "x\n",
	})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	before := waitState(t, w, name, "running")
	idBefore, err := podman(ctx, "inspect", "--format", "{{.Id}}", "ws-"+name)
	if err != nil {
		t.Fatal(err)
	}
	// Something in $HOME that must survive, and a devcontainer change to pick up.
	sh := func(script string) (string, error) {
		return podman(ctx, "exec", "--user", before.User, "ws-"+name, "/bin/sh", "-c", script)
	}
	if _, err := sh("echo keep > ~/marker"); err != nil {
		t.Fatal(err)
	}
	dc, err := podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", "ws-"+name+"-src")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dc, ".devcontainer/Dockerfile"), "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1002 dev2\n")
	writeFile(t, filepath.Join(dc, ".devcontainer/devcontainer.json"), `{"build":{"dockerfile":"Dockerfile"},"remoteUser":"dev2"}`)

	if _, err := w.Rebuild(ctx, name, nil); err != nil {
		t.Fatal(err)
	}
	after := waitState(t, w, name, "running", 5*time.Minute)
	if after.User != "1002:1002" {
		t.Fatalf("after rebuild user = %s, want the new remoteUser (1002)", after.User)
	}
	idAfter, _ := podman(ctx, "inspect", "--format", "{{.Id}}", "ws-"+name)
	if idAfter == idBefore {
		t.Error("the container wasn't recreated")
	}
	out, err := podman(ctx, "exec", "--user", after.User, "ws-"+name, "/bin/sh", "-c", "cat ~/marker; test -f /workspaces/"+repoName(repo)+"/README.md && echo repo-ok")
	if err != nil || !strings.Contains(out, "keep") || !strings.Contains(out, "repo-ok") {
		t.Fatalf("after rebuild: %q, %v (want $HOME and the clone)", out, err)
	}
}

// TestWorkspaceInlineDevcontainer: an inline devcontainer wins over the repo's, leaves the clone
// untouched (git status clean) and survives a rebuild.
func TestWorkspaceInlineDevcontainer(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{
		".devcontainer/devcontainer.json": `{"image":"docker.io/library/alpine:3.22"}`, // root: must be ignored
		"tools/Dockerfile":                "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1003 dev\n",
		"README.md":                       "x\n",
	})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	inline := []byte(`{"build":{"dockerfile":"tools/Dockerfile"},"remoteUser":"dev","postCreateCommand":"touch ~/inline-ran"}`)
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo, Devcontainer: inline}, nil); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		ws := waitState(t, w, name, "running", 5*time.Minute)
		if ws.User != "1003:1003" {
			t.Fatalf("%s: user = %s, want 1003 from the inline devcontainer", when, ws.User)
		}
		if _, err := podman(ctx, "exec", "--user", ws.User, "ws-"+name, "test", "-f", "/home/dev/inline-ran"); err != nil {
			t.Fatalf("%s: postCreateCommand didn't run: %v", when, err)
		}
		src, _ := podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", "ws-"+name+"-src")
		out, err := exec.Command("git", "-C", src, "status", "--porcelain").CombinedOutput()
		if err != nil || len(out) > 0 {
			t.Fatalf("%s: clone not clean: %q, %v", when, out, err)
		}
	}
	check("create")
	if _, err := w.Rebuild(ctx, name, nil); err != nil {
		t.Fatal(err)
	}
	check("rebuild")
}

// TestWorkspaceRebuildUnknown: a name with no volumes is not a workspace.
func TestWorkspaceRebuildUnknown(t *testing.T) {
	needPodman(t)
	if _, err := NewWorkspaces().Rebuild(context.Background(), testName(), nil); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("rebuild unknown: %v, want ErrWorkspaceNotFound", err)
	}
}

// TestWorkspaceEnv: env vars reach the container through an env-file, and a rebuild applies
// the new values.
func TestWorkspaceEnv(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })

	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, []string{"FOO=bar", "DB_URL=postgres://u:p@db/app"}); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	env := func(k string) string {
		out, err := podman(ctx, "exec", "--user", ws.User, "ws-"+name, "printenv", k)
		if err != nil {
			return ""
		}
		return out
	}
	if got := env("FOO"); got != "bar" {
		t.Fatalf("FOO = %q, want bar", got)
	}
	if got := env("DB_URL"); got != "postgres://u:p@db/app" {
		t.Fatalf("DB_URL = %q", got)
	}

	if _, err := w.Rebuild(ctx, name, []string{"FOO=baz"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running", 5*time.Minute)
	if got := env("FOO"); got != "baz" {
		t.Fatalf("after rebuild FOO = %q, want baz", got)
	}
	if got := env("DB_URL"); got != "" {
		t.Fatalf("after rebuild DB_URL = %q, want it gone (env replaced)", got)
	}
}

// TestWorkspaceReachesServiceOnDevNetwork: a service container on the shared network is
// reachable by name from the workspace, and the workspace still reaches the internet.
func TestWorkspaceReachesServiceOnDevNetwork(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	svc := testName()
	if _, err := podman(ctx, "run", "-d", "--name", svc, "--network", DevNetwork, "--entrypoint", "sh",
		"docker.io/library/python:3-alpine", "-c", `mkdir -p /s && echo svc-ok > /s/index.html && exec python3 -m http.server 8080 --directory /s`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { podman(context.Background(), "rm", "-f", "-t", "0", svc) })

	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	out, err := podman(ctx, "exec", "--user", ws.User, "ws-"+name, "curl", "-s", "-m", "5", "http://"+svc+":8080/")
	if err != nil || !strings.Contains(out, "svc-ok") {
		t.Fatalf("workspace -> service: %q, %v", out, err)
	}
}

// A repo's named volume is the workspace's own (ws-<name>-v-<source>), and goes with it.
func TestWorkspaceMountsScoped(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	name := testName()
	victim := "ws-" + name + "x-home" // shaped like another workspace's home
	if _, err := podman(ctx, "volume", "create", victim); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { podman(context.Background(), "volume", "rm", "-f", victim) })
	repo := gitRepo(t, map[string]string{
		".devcontainer/devcontainer.json": `{"image":"docker.io/library/alpine:3.22","mounts":["type=volume,source=cache,target=/cache","type=volume,source=` + victim + `,target=/v"]}`,
	})
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running", 5*time.Minute)
	podman(ctx, "exec", "ws-"+name, "touch", "/v/written")
	if out, _ := podman(ctx, "unshare", "sh", "-c", "ls \"$(podman volume inspect --format '{{.Mountpoint}}' "+victim+")\""); out != "" {
		t.Errorf("wrote into %s: %s", victim, out)
	}
	if err := w.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if out, _ := podman(ctx, "volume", "ls", "-q", "--filter", "name=ws-"+name+"-v-"); out != "" {
		t.Errorf("volumes left: %s", out)
	}
}

// TestWorkspaceSFTPHelper: an image without sftp-server (debian:13) gets the agent's own SFTP
// server copied in once, and a real SFTP session works through the gateway's subsystem script,
// as the workspace user.
func TestWorkspaceSFTPHelper(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	dc := json.RawMessage(`{"image": "docker.io/library/debian:13"}`)
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo, Devcontainer: dc}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	if out, _ := podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "ls /usr/lib/openssh/sftp-server 2>&1"); !strings.Contains(out, "No such file") {
		t.Skip("this debian image ships sftp-server; the test needs one without")
	}

	// Before the helper: the subsystem says what's missing instead of failing silently.
	if out, _ := exec.Command("podman", "exec", "--user", ws.User, "ws-"+name, "/bin/sh", "-c", sftpServer).CombinedOutput(); !strings.Contains(string(out), "no sftp-server in this workspace image") {
		t.Fatalf("without helper: %q", out)
	}

	if err := ensureSFTPHelper(ctx, ws); err != nil {
		t.Fatal(err)
	}
	helpers := func() string {
		out, _ := podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "ls "+sftpHelperGlob)
		return out
	}
	first := helpers()
	if !strings.Contains(first, ".walroos-sftp-"+selfHash()) {
		t.Fatalf("helper not copied: %q", first)
	}
	if err := ensureSFTPHelper(ctx, ws); err != nil || helpers() != first {
		t.Fatalf("second call: %v, helpers %q (want no new copy)", err, helpers())
	}

	// A real SFTP session through the same script the gateway runs.
	cmd := exec.Command("podman", "exec", "-i", "--user", ws.User, "-w", ws.Folder, "ws-"+name, "/bin/sh", "-c", sftpServer)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	c, err := sftp.NewClientPipe(out, in)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	f, err := c.Create(ws.Folder + "/uploaded.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("over sftp\n"))
	f.Close()
	if got, err := podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "cat "+ws.Folder+"/uploaded.txt; stat -c %u:%g "+ws.Folder+"/uploaded.txt"); err != nil || got != "over sftp\n"+ws.User {
		t.Fatalf("uploaded file: %q, %v (want content and owner %s)", got, err, ws.User)
	}
	if entries, err := c.ReadDir(ws.Folder); err != nil || len(entries) < 2 {
		t.Fatalf("readdir: %v, %v", entries, err)
	}
	if _, err := c.Stat("/etc/shadow-does-not-exist"); err == nil {
		t.Error("stat of a missing file succeeded")
	}
}

// TestWorkspaceSFTPHelperMusl: on an Alpine (musl) image a glibc-linked walroos can't run, and
// the subsystem says so (and how to fix it) instead of an opaque "not found".
func TestWorkspaceSFTPHelperMusl(t *testing.T) {
	needPodman(t)
	if out, _ := exec.Command("file", os.Args[0]).Output(); strings.Contains(string(out), "statically linked") {
		t.Skip("this test binary is static: the helper would run on musl too")
	}
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	dc := json.RawMessage(`{"image": "docker.io/library/alpine:3.22"}`)
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo, Devcontainer: dc}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	if err := ensureSFTPHelper(ctx, ws); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("podman", "exec", "--user", ws.User, "ws-"+name, "/bin/sh", "-c", sftpServer).CombinedOutput()
	if !strings.Contains(string(out), "can't run in this image") || !strings.Contains(string(out), "CGO_ENABLED=0") {
		t.Fatalf("glibc helper on musl: %q, want the explanation", out)
	}
}

// TestWorkspaceHomeRepoLink: Zed's Open Folder starts in $HOME, so ~/<repo> links to the clone;
// something the user put at that path is never replaced, not even by a rebuild.
func TestWorkspaceHomeRepoLink(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{
		".devcontainer/Dockerfile":        "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1001 dev\n",
		".devcontainer/devcontainer.json": `{"build":{"dockerfile":"Dockerfile"},"remoteUser":"dev"}`,
	})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	sh := func(script string) (string, error) {
		return podman(ctx, "exec", "--user", ws.User, "ws-"+name, "/bin/sh", "-c", script)
	}
	link := "~/" + repoName(repo)
	if out, err := sh("readlink " + link + "; ls -a " + link + "/"); err != nil || !strings.HasPrefix(out, ws.Folder+"\n") || !strings.Contains(out, ".devcontainer") {
		t.Fatalf("~/<repo>: %q, %v (want a link to %s)", out, err, ws.Folder)
	}
	if out, _ := sh("cd " + ws.Folder + " && git status --porcelain"); out != "" {
		t.Errorf("the link dirtied the clone: %q", out)
	}

	if _, err := sh("rm " + link + " && echo mine > " + link); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Rebuild(ctx, name, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running", 5*time.Minute)
	if out, err := sh("cat " + link); err != nil || out != "mine" {
		t.Errorf("the user's file at ~/<repo> after a rebuild: %q, %v", out, err)
	}
}

// TestWorkspaceNewProject: with init instead of repo_url the node runs git init, stages the files
// (not committed: the first commit is the user's), runs the devcontainer among them, and a rebuild
// keeps the repo without a remote to fall back on.
func TestWorkspaceNewProject(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	w := NewWorkspaces()

	for _, bad := range []WorkspaceSpec{
		{Name: "x", Init: map[string]string{"../escape": "x"}},
		{Name: "x", Init: map[string]string{"/etc/passwd": "x"}},
		{Name: "x", RepoURL: "https://github.com/a/b", Init: map[string]string{"README.md": "x"}},
		{Name: "x"},
	} {
		if _, err := w.Create(ctx, bad, nil); !errors.Is(err, ErrInvalidWorkspace) {
			t.Errorf("create %+v: %v, want ErrInvalidWorkspace", bad, err)
		}
	}

	name := testName()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	files := map[string]string{
		".devcontainer/devcontainer.json": `{"image": "docker.io/library/alpine:3.22"}`,
		"README.md":                       "# " + name + "\n",
	}
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, Init: files}, nil); err != nil {
		t.Fatal(err)
	}
	ws := waitState(t, w, name, "running")
	if ws.Folder != "/workspaces/"+name || !strings.HasPrefix(ws.Image, "docker.io/library/alpine") {
		t.Fatalf("workspace = %+v", ws)
	}
	if out, err := podman(ctx, "exec", "-w", ws.Folder, "ws-"+name, "cat", "README.md"); err != nil || out != "# "+name {
		t.Fatalf("README.md in the container: %q, %v", out, err)
	}

	repo, err := podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", "ws-"+name+"-src")
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := git("status", "--porcelain"); got != "A  .devcontainer/devcontainer.json\nA  README.md" {
		t.Errorf("git status = %q, want both files staged", got)
	}
	if got := git("symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("branch = %q, want main", got)
	}
	if got := git("remote"); got != "" {
		t.Errorf("remotes = %q, want none (the user adds origin)", got)
	}

	// Rebuild: no remote to fall back on, the saved spec is enough; the staged files stay.
	writeFile(t, filepath.Join(repo, "main.go"), "package main\n")
	if _, err := w.Rebuild(ctx, name, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running")
	if out, err := podman(ctx, "exec", "-w", ws.Folder, "ws-"+name, "ls"); err != nil || !strings.Contains(out, "main.go") {
		t.Errorf("after rebuild ls = %q, %v", out, err)
	}
	if got := git("status", "--porcelain"); !strings.Contains(got, "A  README.md") {
		t.Errorf("after rebuild git status = %q", got)
	}
}
