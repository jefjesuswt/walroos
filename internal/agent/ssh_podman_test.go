//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// TestSSHGatewayPodman drives the gateway against a real workspace on the base image: podman
// exec sessions, a terminal through podman's -t, the image's sftp-server, ssh-agent forwarding
// through the mounted socket dir, -L into the workspace's network namespace, and auto-start.
//
//	WALROOS_PODMAN_TEST=1 go test ./internal/agent -run SSHGatewayPodman -v
func TestSSHGatewayPodman(t *testing.T) {
	needPodman(t)
	ctx := context.Background()
	repo := gitRepo(t, map[string]string{"README.md": "x\n"})
	name := testName()
	w := NewWorkspaces()
	t.Cleanup(func() { w.Delete(context.Background(), name) })
	if _, err := w.Create(ctx, WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
		t.Fatal(err)
	}
	waitState(t, w, name, "running", 10*time.Minute)

	srv := NewServer(func(context.Context, string) (string, error) { return "nLaptop", nil }, "tok")
	srv.ws = w
	exe := self(t)
	gw := srv.SSHGateway(func(context.Context, string, string) (string, error) { return "owner@github", nil }, newSigner(t), []string{exe})
	t.Setenv("WALROOS_TEST_RELAY", "1") // the relay command is this binary
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go gw.Serve(ln)

	connect := func() *ssh.Client {
		t.Helper()
		c, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
			User: name, Auth: []ssh.AuthMethod{ssh.PublicKeys(newSigner(t))},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c := connect()
	run := func(cmd string) string {
		t.Helper()
		s, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		out, err := s.CombinedOutput(cmd)
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got, want := run(`whoami; pwd; echo "$SHELL"`), "dev\n/workspaces/"+filepath.Base(repo)+"\n/bin/bash"; got != want {
		t.Errorf("session: %q, want %q", got, want)
	}

	// Terminal: podman exec -t on the gateway's pty, size and resize included.
	s, _ := c.NewSession()
	s.RequestPty("xterm-256color", 30, 90, ssh.TerminalModes{})
	stdin, _ := s.StdinPipe()
	var out bytes.Buffer
	s.Stdout = &out
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(stdin, `stty size; echo "t-$((1+1))"`)
	waitFor(t, &out, "t-2")
	s.WindowChange(40, 120)
	time.Sleep(500 * time.Millisecond) // podman forwards the resize to the container's terminal
	fmt.Fprintln(stdin, "stty size; exit")
	s.Wait()
	if !strings.Contains(out.String(), "30 90") || !strings.Contains(out.String(), "40 120") {
		t.Errorf("terminal sizes: %q", out.String())
	}

	// sftp: the base image's sftp-server answers INIT with VERSION.
	ch, reqs, err := c.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	if ok, _ := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{"sftp"})); !ok {
		t.Fatal("sftp refused")
	}
	ch.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3})
	head := make([]byte, 5)
	if _, err := io.ReadFull(ch, head); err != nil || head[4] != 2 {
		t.Errorf("sftp: %v %v", head, err)
	}
	ch.Close()

	// Agent forwarding: ssh-add inside the workspace sees the laptop's key.
	keyring := sshagent.NewKeyring()
	_, laptopKey, _ := ed25519.GenerateKey(rand.Reader)
	keyring.Add(sshagent.AddedKey{PrivateKey: laptopKey, Comment: "laptop-key"})
	sshagent.ForwardToAgent(c, keyring)
	s, _ = c.NewSession()
	sshagent.RequestAgentForwarding(s)
	if got, err := s.CombinedOutput("ssh-add -l"); err != nil || !strings.Contains(string(got), "laptop-key") {
		t.Errorf("ssh-add -l in the workspace: %q, %v", got, err)
	}

	// -L reaches a port inside the workspace's network namespace, not the host's.
	pid, err := podman(ctx, "inspect", "--format", "{{.State.Pid}}", "ws-"+name)
	if err != nil {
		t.Fatal(err)
	}
	echo := exec.Command("podman", "unshare", "nsenter", "--net=/proc/"+pid+"/ns/net", "--", exe, "127.0.0.1:18080")
	echo.Env = append(os.Environ(), "WALROOS_TEST_ECHO=1", "WALROOS_TEST_RELAY=")
	if err := echo.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { echo.Process.Kill(); echo.Wait() }()
	var conn net.Conn
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if conn, err = c.Dial("tcp", "127.0.0.1:18080"); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("forward into the workspace: %v", err)
	}
	conn.Write([]byte("ping"))
	buf := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Errorf("echo through -L: %q, %v", buf, err)
	}
	conn.Close()
	if hostConn, err := net.DialTimeout("tcp", "127.0.0.1:18080", time.Second); err == nil {
		hostConn.Close()
		t.Error("the echo server answers on the host: the relay didn't run in the workspace's netns")
	}

	// A stopped workspace starts on connect.
	c.Close()
	if err := w.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	c = connect()
	if got := run("echo up"); got != "up" {
		t.Errorf("after auto-start: %q", got)
	}
}
