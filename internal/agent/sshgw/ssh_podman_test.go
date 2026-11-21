//go:build linux

package sshgw

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/agenttest"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/agent/workspaces"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// TestSSHGatewayPodman drives the gateway against real workspaces: one on the base image and
// one on a devcontainer image without sftp-server.
//
//	WALROOS_PODMAN_TEST=1 go test ./internal/agent -run SSHGatewayPodman -v
func TestSSHGatewayPodman(t *testing.T) {
	agenttest.NeedPodman(t)
	ctx := context.Background()
	w := workspaces.New()
	create := func(files map[string]string) (name, repo string) {
		repo, name = agenttest.GitRepo(t, files), agenttest.Name()
		t.Cleanup(func() { w.Delete(context.Background(), name) })
		if _, err := w.Create(ctx, agent.WorkspaceSpec{Name: name, RepoURL: repo}, nil); err != nil {
			t.Fatal(err)
		}
		return name, repo
	}
	name, repo := create(map[string]string{"README.md": "x\n"})
	bare, _ := create(map[string]string{
		".devcontainer/Dockerfile":        "FROM docker.io/library/alpine:3.22\nRUN adduser -D -u 1001 dev\n",
		".devcontainer/devcontainer.json": `{"build": {"dockerfile": "Dockerfile"}, "remoteUser": "dev"}`,
	})
	running(t, w, name)
	running(t, w, bare)

	identify := func(context.Context, string) (string, error) { return "nLaptop", nil }
	exe := self(t)
	t.Setenv("WALROOS_TEST_RELAY", "1") // the relay command is this binary
	gateway := func() *SSHGateway {
		return New(identify, func(context.Context, string, string) (string, error) { return "owner@github", nil }, newSigner(t), []string{exe}, w)
	}
	connect := serveGateway(t, gateway())

	t.Run("session", func(t *testing.T) {
		c := connect(t, name)
		if got, want := runSSH(t, c, `whoami; pwd; echo "$SHELL"`), "dev\n/workspaces/"+filepath.Base(repo)+"\n/bin/bash"; got != want {
			t.Errorf("session: %q, want %q", got, want)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		s, _ := connect(t, name).NewSession()
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
	})

	t.Run("sftp", func(t *testing.T) {
		ch := sftpChannel(t, connect(t, name))
		ch.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3}) // INIT v3
		head := make([]byte, 5)
		if _, err := io.ReadFull(ch, head); err != nil || head[4] != 2 {
			t.Errorf("sftp: %v %v, want VERSION", head, err)
		}
	})

	t.Run("sftp without sftp-server", func(t *testing.T) {
		c := connect(t, bare)
		if got := runSSH(t, c, "whoami"); got != "dev" {
			t.Errorf("whoami on the devcontainer image: %q", got)
		}
		// The image (alpine) has no sftp-server, so the gateway copies the agent's binary in.
		// A static binary runs there and answers SFTP; a glibc one (a plain `go test` build)
		// can't on musl, and the subsystem says why. A glibc image is TestWorkspaceSFTPHelper.
		if out, _ := exec.Command("file", os.Args[0]).Output(); strings.Contains(string(out), "statically linked") {
			ch := sftpChannel(t, c)
			ch.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3}) // INIT v3
			head := make([]byte, 5)
			if _, err := io.ReadFull(ch, head); err != nil || head[4] != 2 {
				t.Errorf("sftp through the helper: %v %v, want VERSION", head, err)
			}
			return
		}
		ch, codes := sessionChannel(t, c)
		if ok, _ := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{"sftp"})); !ok {
			t.Fatal("sftp refused")
		}
		stderr, _ := io.ReadAll(ch.Stderr())
		if code := <-codes; code != 127 || !strings.Contains(string(stderr), "can't run in this image") || !strings.Contains(string(stderr), "CGO_ENABLED=0") {
			t.Errorf("exit %d, stderr %q", code, stderr)
		}
	})

	t.Run("agent forwarding", func(t *testing.T) {
		c := connect(t, name)
		keyring := sshagent.NewKeyring()
		_, laptopKey, _ := ed25519.GenerateKey(rand.Reader)
		keyring.Add(sshagent.AddedKey{PrivateKey: laptopKey, Comment: "laptop-key"})
		sshagent.ForwardToAgent(c, keyring)
		s, _ := c.NewSession()
		sshagent.RequestAgentForwarding(s)
		if got, err := s.CombinedOutput("ssh-add -l"); err != nil || !strings.Contains(string(got), "laptop-key") {
			t.Errorf("ssh-add -l in the workspace: %q, %v", got, err)
		}
	})

	t.Run("agent forwarding on a workspace without agent dir", func(t *testing.T) {
		// Workspaces created before the gateway have no walroos.agentdir label.
		g := gateway()
		lookup := g.lookup
		g.lookup = func(ctx context.Context, name string) (agent.Workspace, error) {
			ws, err := lookup(ctx, name)
			ws.AgentDir = ""
			return ws, err
		}
		c := serveGateway(t, g)(t, name)
		sshagent.ForwardToAgent(c, sshagent.NewKeyring())
		s, _ := c.NewSession()
		if err := sshagent.RequestAgentForwarding(s); err == nil {
			t.Error("agent forwarding accepted without a socket dir")
		}
		if out, err := s.CombinedOutput(`echo "sock=${SSH_AUTH_SOCK:-none}"`); err != nil || strings.TrimSpace(string(out)) != "sock=none" {
			t.Errorf("session after refused -A: %q, %v", out, err)
		}
	})

	t.Run("port forward", func(t *testing.T) {
		c := connect(t, name)
		pid, err := host.Podman(ctx, "inspect", "--format", "{{.State.Pid}}", "ws-"+name)
		if err != nil {
			t.Fatal(err)
		}
		echo := exec.Command("podman", "unshare", "nsenter", "--net=/proc/"+pid+"/ns/net", "--", exe, "0.0.0.0:18080")
		echo.Env = append(os.Environ(), "WALROOS_TEST_ECHO=1", "WALROOS_TEST_RELAY=")
		stop, _ := echo.StdinPipe() // the echo server exits when this closes
		if err := echo.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { stop.Close(); echo.Wait() }()
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

		// Names resolve like inside the workspace: its hostname is only in its /etc/hosts.
		hostname, _ := host.Podman(ctx, "inspect", "--format", "{{.Config.Hostname}}", "ws-"+name)
		if conn, err := c.Dial("tcp", hostname+":18080"); err != nil {
			t.Errorf("forward to the workspace's hostname %q: %v", hostname, err)
		} else {
			conn.Close()
		}
		if hostConn, err := net.DialTimeout("tcp", "127.0.0.1:18080", time.Second); err == nil {
			hostConn.Close()
			t.Error("the echo server answers on the host: the relay didn't run in the workspace's netns")
		}
	})

	t.Run("port forward to a closed port", func(t *testing.T) {
		c := connect(t, name)
		for addr, want := range map[string]string{"127.0.0.1:1": "connection refused", "nohost.invalid:80": "nohost.invalid"} {
			start := time.Now()
			conn, err := c.Dial("tcp", addr)
			if err == nil {
				conn.Close()
				t.Errorf("%s: forward opened", addr)
			} else if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v, want it to say %q", addr, err, want)
			}
			// Only the refused port must fail fast: an unknown name takes as long as the
			// workspace's DNS takes to say NXDOMAIN (~10s under WSL, same as nslookup in any container).
			if d := time.Since(start); addr == "127.0.0.1:1" && d > 5*time.Second {
				t.Errorf("%s: took %v to fail", addr, d)
			}
		}
		if got := runSSH(t, c, "echo still-up"); got != "still-up" {
			t.Errorf("connection after failed forwards: %q", got)
		}
	})

	// Processes a session started must not outlive it: a dev server or `claude` left running
	// after the laptop closed would pile up in the workspace.
	running := func(marker string) bool {
		_, err := host.Podman(ctx, "exec", "ws-"+name, "pgrep", "-f", marker)
		return err == nil
	}
	gone := func(t *testing.T, marker string) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); running(marker); time.Sleep(200 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%q still runs in the workspace after the session ended", marker)
			}
		}
	}
	started := func(t *testing.T, marker string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !running(marker); time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%q never started", marker)
			}
		}
	}

	t.Run("connection dropped during exec", func(t *testing.T) {
		c := connect(t, name)
		s, _ := c.NewSession()
		if err := s.Start("sleep 4242"); err != nil {
			t.Fatal(err)
		}
		started(t, "sleep 4242")
		c.Close()
		gone(t, "sleep 4242")
	})

	t.Run("connection dropped during a terminal", func(t *testing.T) {
		c := connect(t, name)
		s, _ := c.NewSession()
		s.RequestPty("xterm", 24, 80, ssh.TerminalModes{})
		stdin, _ := s.StdinPipe()
		if err := s.Shell(); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(stdin, "sleep 4343")
		started(t, "sleep 4343")
		c.Close()
		gone(t, "sleep 4343")
	})

	t.Run("session closed, connection still open", func(t *testing.T) {
		c := connect(t, name)
		s, _ := c.NewSession()
		if err := s.Start("sleep 4444"); err != nil {
			t.Fatal(err)
		}
		started(t, "sleep 4444")
		s.Close()
		gone(t, "sleep 4444")
		if got := runSSH(t, c, "echo still-up"); got != "still-up" {
			t.Errorf("connection after closing a session: %q", got)
		}
	})

	t.Run("stopped workspace starts on connect", func(t *testing.T) {
		if err := w.Stop(ctx, name); err != nil {
			t.Fatal(err)
		}
		if got := runSSH(t, connect(t, name), "echo up"); got != "up" {
			t.Errorf("after auto-start: %q", got)
		}
	})
}

// serveGateway serves g on a loopback port and returns how to connect to a workspace on it.
func serveGateway(t *testing.T, g *SSHGateway) func(t *testing.T, workspace string) *ssh.Client {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go g.Serve(ln)
	return func(t *testing.T, workspace string) *ssh.Client {
		t.Helper()
		c, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
			User: workspace, Auth: []ssh.AuthMethod{ssh.PublicKeys(newSigner(t))},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
}

func runSSH(t *testing.T, c *ssh.Client, cmd string) string {
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

// sessionChannel opens a raw session channel, like sftp clients use, and reports its exit status.
func sessionChannel(t *testing.T, c *ssh.Client) (ssh.Channel, chan uint32) {
	t.Helper()
	ch, reqs, err := c.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ch.Close() })
	codes := make(chan uint32, 1)
	go func() {
		for r := range reqs {
			if r.Type == "exit-status" && len(r.Payload) == 4 {
				codes <- binary.BigEndian.Uint32(r.Payload)
			}
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}()
	return ch, codes
}

func sftpChannel(t *testing.T, c *ssh.Client) ssh.Channel {
	t.Helper()
	ch, _ := sessionChannel(t, c)
	if ok, _ := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{"sftp"})); !ok {
		t.Fatal("sftp refused")
	}
	return ch
}
