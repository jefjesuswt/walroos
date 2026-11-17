//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// TestMain doubles as helper processes, run as `agent.test ADDR`:
//   - WALROOS_TEST_RELAY=1: Relay, like production runs `walroos node relay` in the workspace.
//   - WALROOS_TEST_ECHO=1: a TCP echo server, the app a forwarded port reaches.
func TestMain(m *testing.M) {
	switch {
	case os.Getenv("WALROOS_TEST_RELAY") != "":
		if err := Relay(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case os.Getenv("WALROOS_TEST_ECHO") != "":
		ln, err := net.Listen("tcp", os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for {
			conn, err := ln.Accept()
			if err != nil {
				os.Exit(1)
			}
			go func() { io.Copy(conn, conn); conn.Close() }()
		}
	}
	os.Exit(m.Run())
}

// self is this test binary, for helper processes.
func self(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

type testGateway struct {
	addr      string
	userKey   ssh.Signer
	agentDir  string
	wsErr     atomic.Value // string: the error looking up the workspace returns, "" for none
	tailnet   atomic.Bool  // whether identify accepts the caller
	authCalls atomic.Int32
}

// newTestGateway serves a gateway whose only workspace, "app", runs commands on this host:
// everything the gateway does except podman itself.
func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	tg := &testGateway{agentDir: t.TempDir()}
	tg.tailnet.Store(true)
	tg.wsErr.Store("")
	tg.userKey = newSigner(t)
	folder := t.TempDir()
	emptyHome := t.TempDir()
	for _, f := range []string{".zshrc", ".zshenv", ".bash_profile", ".bashrc"} {
		if err := os.WriteFile(filepath.Join(emptyHome, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	g := newSSHGateway(
		func(context.Context, string) (string, error) {
			if !tg.tailnet.Load() {
				return "", errors.New("not a tailnet peer")
			}
			return "nLaptop", nil
		},
		func(_ context.Context, ws, fp string) (string, error) {
			tg.authCalls.Add(1)
			if ws == "app" && fp == ssh.FingerprintSHA256(tg.userKey.PublicKey()) {
				return "owner@github", nil
			}
			return "", errors.New("denied")
		},
		newSigner(t),
	)
	g.lookup = func(_ context.Context, name string) (Workspace, error) {
		// Host and container paths are the same here, so AgentDir is the host dir.
		ws := Workspace{Name: name, State: "running", Folder: folder, AgentDir: tg.agentDir}
		if msg := tg.wsErr.Load().(string); msg != "" {
			return ws, errors.New(msg)
		}
		return ws, nil
	}
	g.command = func(ctx context.Context, ws Workspace, _ bool, env, argv []string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = ws.Folder
		// Like a container: no host ssh-agent and no host dotfiles (one exporting SSH_AUTH_SOCK
		// would hide the gateway's). Empty rc files keep zsh from its first-run wizard. Then
		// the request's env, as podman does.
		cmd.Env = append(cleanEnv(), "HOME="+emptyHome)
		cmd.Env = append(cmd.Env, env...)
		return cmd
	}
	exe := self(t)
	g.dial = func(ctx context.Context, _ Workspace, addr string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, exe, addr)
		cmd.Env = append(os.Environ(), "WALROOS_TEST_RELAY=1")
		return cmd, nil
	}
	g.hostDir = func(string) string { return tg.agentDir }

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go g.Serve(ln)
	tg.addr = ln.Addr().String()
	return tg
}

func (tg *testGateway) dial(t *testing.T, user string, key ssh.Signer) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", tg.addr, &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
}

func (tg *testGateway) client(t *testing.T) *ssh.Client {
	t.Helper()
	c, err := tg.dial(t, "app", tg.userKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSSHAuth(t *testing.T) {
	tg := newTestGateway(t)
	if _, err := tg.dial(t, "app", newSigner(t)); err == nil {
		t.Error("a key the hub doesn't know got in")
	}
	if _, err := tg.dial(t, "other", tg.userKey); err == nil {
		t.Error("a key got into a workspace the hub didn't allow it on")
	}
	if _, err := tg.dial(t, "Not_A_Name", tg.userKey); err == nil {
		t.Error("an invalid workspace name got in")
	}
	calls := tg.authCalls.Load()
	tg.client(t)
	tg.client(t)
	if n := tg.authCalls.Load() - calls; n != 1 {
		t.Errorf("hub asked %d times for the same key, want once (cached)", n)
	}

	tg.tailnet.Store(false)
	if _, err := tg.dial(t, "app", tg.userKey); err == nil {
		t.Error("a connection from outside the tailnet got in")
	}
}

func TestSSHExec(t *testing.T) {
	tg := newTestGateway(t)
	c := tg.client(t)

	s, _ := c.NewSession()
	s.Setenv("WALROOS_X", "from-client")
	out, err := s.Output(`echo "$WALROOS_X"; pwd; test -n "$SSH_CONNECTION" && echo conn`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(string(out)), "\n"); len(got) != 3 || got[0] != "from-client" || got[2] != "conn" {
		t.Errorf("exec output %q", out)
	}

	s, _ = c.NewSession()
	var ee *ssh.ExitError
	if err := s.Run("echo oops >&2; exit 3"); !errors.As(err, &ee) || ee.ExitStatus() != 3 {
		t.Errorf("exit status: %v, want 3", err)
	}

	// stdin reaches the command and its EOF ends it.
	s, _ = c.NewSession()
	s.Stdin = strings.NewReader("line 1\nline 2\n")
	if out, err := s.Output("wc -l"); err != nil || strings.TrimSpace(string(out)) != "2" {
		t.Errorf("stdin: %q, %v", out, err)
	}

	// A command that exits without reading stdin must not wait for the client's EOF.
	s, _ = c.NewSession()
	pr, pw := io.Pipe()
	defer pw.Close()
	s.Stdin = pr
	done := make(chan error, 1)
	go func() { done <- s.Run("true") }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run with open stdin: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("session hung waiting for stdin EOF after the command exited")
	}
}

func TestSSHPty(t *testing.T) {
	tg := newTestGateway(t)
	s, _ := tg.client(t).NewSession()
	if err := s.RequestPty("xterm-test", 40, 100, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	stdin, _ := s.StdinPipe()
	var out bytes.Buffer
	s.Stdout = &out
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(stdin, `stty size; echo "term=$TERM"; tty -s && echo "tty-$((1+1))"`)
	waitFor(t, &out, "tty-2") // not in the echoed input
	if !strings.Contains(out.String(), "40 100") || !strings.Contains(out.String(), "term=xterm-test") {
		t.Fatalf("pty output %q", out.String())
	}
	if err := s.WindowChange(50, 120); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(stdin, "stty size; exit 4")
	var ee *ssh.ExitError
	if err := s.Wait(); !errors.As(err, &ee) || ee.ExitStatus() != 4 {
		t.Errorf("shell exit: %v, want 4", err)
	}
	if !strings.Contains(out.String(), "50 120") {
		t.Errorf("after window-change: %q", out.String())
	}
}

func waitFor(t *testing.T, buf *bytes.Buffer, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q in %q", s, buf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSSHSftp(t *testing.T) {
	tg := newTestGateway(t)
	c := tg.client(t)
	// A raw channel, like sftp clients use: subsystem request, then packets on stdin/stdout.
	open := func(subsystem string) (ssh.Channel, chan uint32, bool) {
		ch, reqs, err := c.OpenChannel("session", nil)
		if err != nil {
			t.Fatal(err)
		}
		exit := make(chan uint32, 1)
		go func() {
			for r := range reqs {
				if r.Type == "exit-status" && len(r.Payload) == 4 {
					exit <- binary.BigEndian.Uint32(r.Payload)
				}
			}
		}()
		ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{subsystem}))
		if err != nil {
			t.Fatal(err)
		}
		return ch, exit, ok
	}

	if _, _, ok := open("nope"); ok {
		t.Error("unknown subsystem accepted")
	}

	ch, exit, ok := open("sftp")
	if !ok {
		t.Fatal("sftp subsystem refused")
	}
	// SSH_FXP_INIT, version 3. The answer is SSH_FXP_VERSION (2), or the "no sftp-server"
	// message when this host has none.
	ch.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3})
	head := make([]byte, 5)
	if _, err := io.ReadFull(ch, head); err == nil {
		if head[4] != 2 {
			t.Fatalf("sftp answered packet type %d, want 2 (version)", head[4])
		}
		return
	}
	stderr, _ := io.ReadAll(ch.Stderr())
	select {
	case code := <-exit:
		if code != 127 || !strings.Contains(string(stderr), "no sftp-server") {
			t.Fatalf("without sftp-server: exit %d, stderr %q", code, stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit-status")
	}
	t.Log("no sftp-server on this host: checked the error path only")
}

func TestSSHPortForward(t *testing.T) {
	tg := newTestGateway(t)
	c := tg.client(t)

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(conn, conn); conn.Close() }()
		}
	}()

	conn, err := c.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("ping"))
	buf := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("through the tunnel: %q, %v", buf, err)
	}
	conn.Close()

	closed := echo.Addr().(*net.TCPAddr)
	echo.Close()
	if _, err := c.Dial("tcp", closed.String()); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("forward to a closed port: %v, want the connection refused", err)
	}
}

func TestSSHAgentForwarding(t *testing.T) {
	tg := newTestGateway(t)
	c := tg.client(t)

	keyring := sshagent.NewKeyring()
	_, laptopKey, _ := ed25519.GenerateKey(rand.Reader)
	keyring.Add(sshagent.AddedKey{PrivateKey: laptopKey, Comment: "laptop"})
	if err := sshagent.ForwardToAgent(c, keyring); err != nil {
		t.Fatal(err)
	}

	s, _ := c.NewSession()
	if err := sshagent.RequestAgentForwarding(s); err != nil {
		t.Fatal(err)
	}
	out, err := s.Output("printenv SSH_AUTH_SOCK")
	if err != nil {
		t.Fatal(err)
	}
	sock := strings.TrimSpace(string(out))
	if !strings.HasPrefix(sock, tg.agentDir+"/") {
		t.Fatalf("SSH_AUTH_SOCK = %q, want a socket in the workspace's agent dir", sock)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v, %v, want mode 0600", fi, err)
	}

	// What `git push` inside the workspace would do: ask the agent behind SSH_AUTH_SOCK.
	uc, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := sshagent.NewClient(uc).List()
	uc.Close()
	if err != nil || len(keys) != 1 || keys[0].Comment != "laptop" {
		t.Fatalf("keys through the forwarded agent: %v, %v", keys, err)
	}

	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for _, err := os.Stat(sock); err == nil; _, err = os.Stat(sock) {
		if time.Now().After(deadline) {
			t.Fatal("agent socket left behind after the connection closed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Without -A there is no SSH_AUTH_SOCK.
	s, _ = tg.client(t).NewSession()
	if out, _ := s.Output("printenv SSH_AUTH_SOCK || echo none"); strings.TrimSpace(string(out)) != "none" {
		t.Errorf("SSH_AUTH_SOCK without agent forwarding: %q", out)
	}
}

func TestSSHWorkspaceNotReady(t *testing.T) {
	tg := newTestGateway(t)
	tg.wsErr.Store("workspace app is creating, try again in a moment")
	s, _ := tg.client(t).NewSession()
	var stderr bytes.Buffer
	s.Stderr = &stderr
	var ee *ssh.ExitError
	if err := s.Run("true"); !errors.As(err, &ee) || ee.ExitStatus() != 1 || !strings.Contains(stderr.String(), "is creating") {
		t.Errorf("not ready: %v, stderr %q", err, stderr.String())
	}
}

func TestLoadOrCreateHostKey(t *testing.T) {
	file := t.TempDir() + "/key"
	a, err := LoadOrCreateHostKey(file)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateHostKey(file)
	if err != nil {
		t.Fatal(err)
	}
	if ssh.FingerprintSHA256(a.PublicKey()) != ssh.FingerprintSHA256(b.PublicKey()) {
		t.Error("host key changed between runs")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("host key mode %v, want 0600", fi.Mode().Perm())
	}
}

// cleanEnv is os.Environ without what a container wouldn't have: the host's ssh-agent socket
// and SSH_CONNECTION.
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SSH_AUTH_SOCK=") || strings.HasPrefix(kv, "SSH_CONNECTION=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
