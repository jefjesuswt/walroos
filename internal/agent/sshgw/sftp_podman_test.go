package sshgw

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/agenttest"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/agent/workspaces"
	"github.com/pkg/sftp"
)

// TestWorkspaceSFTPHelper: an image without sftp-server (debian:13) gets the agent's own SFTP
// server copied in once, and a real SFTP session works through the gateway's subsystem script,
// as the workspace user.
func TestWorkspaceSFTPHelper(t *testing.T) {
	agenttest.NeedPodman(t)
	ctx := context.Background()
	repo := agenttest.GitRepo(t, map[string]string{"README.md": "x\n"})
	name := agenttest.Name()
	w := workspaces.New()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	dc := json.RawMessage(`{"image": "docker.io/library/debian:13"}`)
	if _, err := w.Create(ctx, agent.WorkspaceSpec{Name: name, RepoURL: repo, Devcontainer: dc}, nil); err != nil {
		t.Fatal(err)
	}
	ws := running(t, w, name)
	if out, _ := host.Podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "ls /usr/lib/openssh/sftp-server 2>&1"); !strings.Contains(out, "No such file") {
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
		out, _ := host.Podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "ls "+sftpHelperGlob)
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
	if got, err := host.Podman(ctx, "exec", "ws-"+name, "/bin/sh", "-c", "cat "+ws.Folder+"/uploaded.txt; stat -c %u:%g "+ws.Folder+"/uploaded.txt"); err != nil || got != "over sftp\n"+ws.User {
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
	agenttest.NeedPodman(t)
	if out, _ := exec.Command("file", os.Args[0]).Output(); strings.Contains(string(out), "statically linked") {
		t.Skip("this test binary is static: the helper would run on musl too")
	}
	ctx := context.Background()
	repo := agenttest.GitRepo(t, map[string]string{"README.md": "x\n"})
	name := agenttest.Name()
	w := workspaces.New()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	dc := json.RawMessage(`{"image": "docker.io/library/alpine:3.22"}`)
	if _, err := w.Create(ctx, agent.WorkspaceSpec{Name: name, RepoURL: repo, Devcontainer: dc}, nil); err != nil {
		t.Fatal(err)
	}
	ws := running(t, w, name)
	if err := ensureSFTPHelper(ctx, ws); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("podman", "exec", "--user", ws.User, "ws-"+name, "/bin/sh", "-c", sftpServer).CombinedOutput()
	if !strings.Contains(string(out), "can't run in this image") || !strings.Contains(string(out), "CGO_ENABLED=0") {
		t.Fatalf("glibc helper on musl: %q, want the explanation", out)
	}
}

// running waits for the workspace to run and returns it.
func running(t *testing.T, w *workspaces.Workspaces, name string) agent.Workspace {
	t.Helper()
	var ws agent.Workspace
	agenttest.WaitState(t, func() (string, error) {
		var err error
		ws, err = w.Get(context.Background(), name)
		return ws.State, err
	}, "running", 5*time.Minute)
	return ws
}
