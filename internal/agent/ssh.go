package agent

// The SSH gateway: one SSH server per node, on the tailnet. The SSH user is the workspace name
// and every session is a `podman exec` into that workspace's container, so images need no sshd.
// Keys are checked with the hub, which owns users and their keys.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHPort is where node agents serve SSH for workspaces.
const SSHPort = "2222"

// authCacheTTL is how long a key the hub accepted keeps working without asking again, so a
// burst of connections (VS Code opens several) doesn't hit the hub each time.
const authCacheTTL = time.Minute

// SSHAuthorize asks the hub whether the key with this fingerprint may open workspace, and
// returns the hub login that owns the key.
type SSHAuthorize func(ctx context.Context, workspace, fingerprint string) (login string, err error)

type SSHGateway struct {
	identify  Identify
	authorize SSHAuthorize
	config    *ssh.ServerConfig

	// How sessions reach workspaces. Podman in production; tests run commands on the host.
	lookup  func(ctx context.Context, name string) (Workspace, error)                       // running workspace (starts it if stopped)
	command func(ctx context.Context, ws Workspace, tty bool, env, argv []string) *exec.Cmd // process inside the workspace
	dial    func(ctx context.Context, ws Workspace, addr string) (*exec.Cmd, error)         // Relay process in the workspace's network
	hostDir func(name string) string                                                        // host side of ws.AgentDir

	mu    sync.Mutex
	cache map[string]cachedAuth
}

type cachedAuth struct {
	login string
	until time.Time
}

// SSHGateway serves SSH into this node's workspaces. relay is the command that runs Relay
// (`walroos node relay`); the gateway appends host:port.
func (s *Server) SSHGateway(authorize SSHAuthorize, hostKey ssh.Signer, relay []string) *SSHGateway {
	g := newSSHGateway(s.identify, authorize, hostKey)
	g.lookup = s.ws.ready
	g.command = podmanExec
	g.dial = func(ctx context.Context, ws Workspace, addr string) (*exec.Cmd, error) {
		return podmanRelay(ctx, ws, relay, addr)
	}
	g.hostDir = agentDir
	return g
}

func newSSHGateway(identify Identify, authorize SSHAuthorize, hostKey ssh.Signer) *SSHGateway {
	g := &SSHGateway{identify: identify, authorize: authorize, cache: map[string]cachedAuth{}}
	g.config = &ssh.ServerConfig{PublicKeyCallback: g.publicKey, ServerVersion: "SSH-2.0-walroos"}
	g.config.AddHostKey(hostKey)
	return g
}

// Serve accepts connections until ln is closed. Only tailnet peers get to the SSH handshake.
func (g *SSHGateway) Serve(ln net.Listener) error {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go g.handle(nc)
	}
}

func (g *SSHGateway) handle(nc net.Conn) {
	defer nc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ends every process this connection started

	if _, err := g.identify(ctx, nc.RemoteAddr().String()); err != nil {
		slog.Warn("SSH: connection from outside the tailnet refused", "remote", nc.RemoteAddr())
		return
	}
	nc.SetDeadline(time.Now().Add(30 * time.Second))
	sc, chans, reqs, err := ssh.NewServerConn(nc, g.config)
	if err != nil {
		return
	}
	defer sc.Close()
	nc.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs) // keepalives get a "no", which OpenSSH accepts; -R forwarding is refused
	slog.Info("SSH: connected", "workspace", sc.User(), "login", sc.Permissions.Extensions["login"], "remote", nc.RemoteAddr())

	c := &sshConn{g: g, sc: sc, ctx: ctx, name: sc.User()}
	defer c.close()
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			go c.session(nch)
		case "direct-tcpip":
			go c.directTCPIP(nch)
		default:
			nch.Reject(ssh.UnknownChannelType, "only sessions and local port forwarding (-L)")
		}
	}
}

func (g *SSHGateway) publicKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	workspace, fp := meta.User(), ssh.FingerprintSHA256(key)
	if !workspaceNameRe.MatchString(workspace) {
		return nil, errors.New("ssh user must be a workspace name")
	}
	k := workspace + " " + fp
	g.mu.Lock()
	cached, ok := g.cache[k]
	g.mu.Unlock()
	if ok && time.Now().Before(cached.until) {
		return &ssh.Permissions{Extensions: map[string]string{"login": cached.login}}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	login, err := g.authorize(ctx, workspace, fp)
	if err != nil {
		// Also how an unreachable hub shows up: every login fails until it's back.
		slog.Warn("SSH: key refused", "workspace", workspace, "fingerprint", fp, "remote", meta.RemoteAddr(), "err", err)
		return nil, err
	}
	g.mu.Lock()
	g.cache[k] = cachedAuth{login: login, until: time.Now().Add(authCacheTTL)}
	g.mu.Unlock()
	return &ssh.Permissions{Extensions: map[string]string{"login": login}}, nil
}

// sshConn is one client connection to one workspace.
type sshConn struct {
	g    *SSHGateway
	sc   *ssh.ServerConn
	ctx  context.Context
	name string

	wsOnce sync.Once
	ws     Workspace
	wsErr  error

	agentOnce sync.Once
	agentSock string // SSH_AUTH_SOCK inside the container, "" if forwarding isn't possible
	agentLn   net.Listener
}

// workspace finds the workspace once per connection, starting it if it was stopped.
func (c *sshConn) workspace() (Workspace, error) {
	c.wsOnce.Do(func() { c.ws, c.wsErr = c.g.lookup(c.ctx, c.name) })
	return c.ws, c.wsErr
}

func (c *sshConn) close() {
	if c.agentLn != nil {
		c.agentLn.Close() // also removes the socket file
	}
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type ptyRequest struct {
	Term                      string
	Cols, Rows, Width, Height uint32
	Modes                     string
}

func (c *sshConn) session(nch ssh.NewChannel) {
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	// Per session, not per connection: closing one session (VS Code and ControlMaster keep the
	// connection open) hangs up its process.
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()

	var (
		env       []string
		tty       *ptyRequest
		ptmx      *os.File
		wantAgent bool
		started   bool
	)
	for req := range reqs {
		ok := false
		switch req.Type {
		case "env":
			var kv struct{ Name, Value string }
			if ssh.Unmarshal(req.Payload, &kv) == nil && envNameRe.MatchString(kv.Name) {
				env = append(env, kv.Name+"="+kv.Value)
				ok = true
			}
		case "pty-req":
			var p ptyRequest
			if !started && ssh.Unmarshal(req.Payload, &p) == nil {
				tty, ok = &p, true
			}
		case "window-change":
			var wc struct{ Cols, Rows, Width, Height uint32 }
			if ptmx != nil && ssh.Unmarshal(req.Payload, &wc) == nil {
				ok = setWinsize(ptmx, wc.Cols, wc.Rows) == nil
			}
		case "auth-agent-req@openssh.com":
			ws, err := c.workspace()
			wantAgent = err == nil && ws.AgentDir != "" // workspaces created before the gateway have no socket dir
			ok = wantAgent
		case "shell", "exec", "subsystem":
			argv, valid := sessionArgv(req)
			if started || !valid {
				break
			}
			started, ok = true, true
			req.Reply(true, nil) // before the process can send exit-status
			req.WantReply = false
			if ptmx, err = c.start(ctx, ch, tty, env, argv, wantAgent); err != nil {
				fmt.Fprintf(ch.Stderr(), "walroos: %v\r\n", err)
				sendExit(ch, 1)
			}
		}
		if req.WantReply {
			req.Reply(ok, nil)
		}
	}
}

// userShell finds the user's login shell in the container's passwd, like sshd would, and sets
// the variables sshd sets. /bin/sh if there's none.
const userShell = `s=$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f7); [ -x "$s" ] || s=/bin/sh; ` +
	`u=$(id -un 2>/dev/null) && export USER="$u" LOGNAME="$u"; export SHELL="$s"; `

// sftpServer runs the image's sftp-server; the walroos base image ships one.
const sftpServer = `for p in /usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server /usr/lib/ssh/sftp-server /usr/libexec/sftp-server /usr/lib/sftp-server; do ` +
	`[ -x "$p" ] && exec "$p"; done; echo "no sftp-server in this workspace image (install openssh-sftp-server)" >&2; exit 127`

// sessionArgv is what runs for a shell, exec or subsystem request.
func sessionArgv(req *ssh.Request) ([]string, bool) {
	switch req.Type {
	case "shell":
		return []string{"/bin/sh", "-c", userShell + `exec "$s" -l`}, true
	case "exec":
		var e struct{ Command string }
		if ssh.Unmarshal(req.Payload, &e) != nil {
			return nil, false
		}
		return []string{"/bin/sh", "-c", userShell + `exec "$s" -c "$1"`, "sh", e.Command}, true
	default: // subsystem
		var s struct{ Name string }
		if ssh.Unmarshal(req.Payload, &s) != nil || s.Name != "sftp" {
			return nil, false
		}
		return []string{"/bin/sh", "-c", sftpServer}, true
	}
}

// start runs argv in the workspace, wired to the channel, and sends exit-status when it ends.
// With a pty it returns the terminal's master side for window-change.
func (c *sshConn) start(ctx context.Context, ch ssh.Channel, tty *ptyRequest, env, argv []string, agent bool) (*os.File, error) {
	ws, err := c.workspace()
	if err != nil {
		return nil, err
	}
	local, ok1 := c.sc.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.sc.RemoteAddr().(*net.TCPAddr)
	if ok1 && ok2 {
		env = append(env, fmt.Sprintf("SSH_CONNECTION=%s %d %s %d", remote.IP, remote.Port, local.IP, local.Port))
	}
	if tty != nil {
		env = append(env, "TERM="+tty.Term)
	}
	if agent {
		if sock := c.agentSocket(ws); sock != "" {
			env = append(env, "SSH_AUTH_SOCK="+sock)
		}
	}

	cmd := c.g.command(ctx, ws, tty != nil, env, argv)
	// When the client goes away, hang up like a closed terminal would; kill if that's ignored.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGHUP) }
	cmd.WaitDelay = 5 * time.Second

	if tty != nil {
		ptmx, err := startPTY(cmd, tty.Cols, tty.Rows)
		if err != nil {
			return nil, err
		}
		go func() {
			go io.Copy(ptmx, ch)
			io.Copy(ch, ptmx) // ends (EIO) once the process exits and the terminal closes
			sendExit(ch, exitCode(cmd.Wait()))
			ptmx.Close()
		}()
		return ptmx, nil
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Not cmd.Stdin = ch: Wait would block until the client sends EOF, even after the process exited.
	go func() {
		io.Copy(stdin, ch)
		stdin.Close()
	}()
	go func() { sendExit(ch, exitCode(cmd.Wait())) }()
	return nil, nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return ee.ExitCode()
	default:
		return 255 // killed by a signal, or never ran
	}
}

func sendExit(ch ssh.Channel, code int) {
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
	ch.Close()
}

// directTCPIP is `ssh -L` (and what VS Code Remote-SSH uses to reach its server): a TCP
// connection to host:port as seen from inside the workspace.
func (c *sshConn) directTCPIP(nch ssh.NewChannel) {
	var d struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nch.ExtraData(), &d); err != nil || d.Port == 0 || d.Port > 65535 {
		nch.Reject(ssh.ConnectionFailed, "invalid forwarding request")
		return
	}
	ws, err := c.workspace()
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	cmd, err := c.g.dial(c.ctx, ws, net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port))))
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		nch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()

	// Relay writes one byte once it's connected: refuse the channel if it couldn't connect,
	// so the client sees "connection refused" instead of an open channel that closes.
	if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
		cmd.Wait()
		nch.Reject(ssh.ConnectionFailed, cmpOr(strings.TrimSpace(stderr.String()), "connect failed"))
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	go ssh.DiscardRequests(reqs)
	go func() {
		io.Copy(stdin, ch)
		stdin.Close()
	}()
	io.Copy(ch, stdout)
	ch.CloseWrite()
}

// agentSocket makes the client's forwarded ssh-agent (ssh -A) reachable inside the workspace:
// a unix socket in the directory bind-mounted at ws.AgentDir. Each connection to it opens an
// auth-agent channel back to the client, so `git push` in the workspace signs with the laptop's
// keys and no key is ever copied to the node.
func (c *sshConn) agentSocket(ws Workspace) string {
	c.agentOnce.Do(func() {
		dir := c.g.hostDir(ws.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			slog.Warn("SSH: agent forwarding unavailable", "workspace", ws.Name, "err", err)
			return
		}
		b := make([]byte, 4)
		rand.Read(b)
		file := hex.EncodeToString(b) + ".sock"
		ln, err := net.Listen("unix", filepath.Join(dir, file))
		if err != nil {
			slog.Warn("SSH: agent forwarding unavailable", "workspace", ws.Name, "err", err)
			return
		}
		os.Chmod(filepath.Join(dir, file), 0o600)
		c.agentLn, c.agentSock = ln, path.Join(ws.AgentDir, file)

		go func() {
			for {
				uc, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer uc.Close()
					ch, reqs, err := c.sc.OpenChannel("auth-agent@openssh.com", nil)
					if err != nil {
						return
					}
					defer ch.Close()
					go ssh.DiscardRequests(reqs)
					go func() {
						io.Copy(ch, uc)
						ch.CloseWrite()
					}()
					io.Copy(uc, ch)
				}()
			}
		}()
	})
	return c.agentSock
}

// ready returns the workspace if it can take sessions, starting it if it was stopped.
func (w *Workspaces) ready(ctx context.Context, name string) (Workspace, error) {
	ws, err := w.Get(ctx, name)
	if err != nil {
		return ws, err
	}
	switch ws.State {
	case "running":
		return ws, nil
	case "stopped":
		if err := w.Start(ctx, name); err != nil {
			return ws, fmt.Errorf("start workspace: %w", err)
		}
		ws.State = "running"
		return ws, nil
	case "failed":
		return ws, fmt.Errorf("workspace %s failed: %s", name, ws.Error)
	default:
		return ws, fmt.Errorf("workspace %s is %s, try again in a moment", name, ws.State)
	}
}

func podmanExec(ctx context.Context, ws Workspace, tty bool, env, argv []string) *exec.Cmd {
	args := []string{"exec", "-i"}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, "--user", ws.User, "-w", ws.Folder)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(append(args, "ws-"+ws.Name), argv...)
	return exec.CommandContext(ctx, "podman", args...)
}

// podmanRelay runs relay inside the workspace's network namespace. Rootless podman owns that
// namespace from its user namespace, so nsenter has to run under `podman unshare`.
// ponytail: two extra processes per forwarded connection; fine for dev ports and VS Code.
func podmanRelay(ctx context.Context, ws Workspace, relay []string, addr string) (*exec.Cmd, error) {
	pid, err := podman(ctx, "inspect", "--format", "{{.State.Pid}}", "ws-"+ws.Name)
	if err != nil || pid == "" || pid == "0" {
		return nil, fmt.Errorf("workspace %s is not running", ws.Name)
	}
	args := append([]string{"nsenter", "--net=/proc/" + pid + "/ns/net", "--"}, relay...)
	args = append(args, "/proc/"+pid+"/root", addr)
	if os.Geteuid() != 0 {
		args = append([]string{"podman", "unshare"}, args...)
	}
	return exec.CommandContext(ctx, args[0], args[1:]...), nil
}

// Relay connects to addr and pipes the connection to stdin/stdout. It writes one 0 byte first,
// so the gateway knows the connection is up. `walroos node relay` runs it inside a workspace's
// network namespace, with root the workspace's filesystem ("" to skip): names then resolve
// like they do in the workspace (its DNS, host.containers.internal), not like on the host.
func Relay(root, addr string) error {
	if root != "" {
		if err := chroot(root); err != nil {
			return fmt.Errorf("enter the workspace's filesystem: %w", err)
		}
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := os.Stdout.Write([]byte{0}); err != nil {
		return err
	}
	go func() {
		io.Copy(conn, os.Stdin)
		conn.(*net.TCPConn).CloseWrite()
	}()
	_, err = io.Copy(os.Stdout, conn)
	return err
}

// LoadOrCreateHostKey reads the gateway's ed25519 host key, creating it (0600) on first run.
func LoadOrCreateHostKey(file string) (ssh.Signer, error) {
	if data, err := os.ReadFile(file); err == nil {
		return ssh.ParsePrivateKey(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "walroos ssh gateway")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}
