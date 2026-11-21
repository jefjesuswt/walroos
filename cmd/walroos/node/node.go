package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
)

// state is what `node join` saves and `node run` reads.
type state struct {
	Hub   string `json:"hub"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

var Cmd = &cobra.Command{
	Use:   "node",
	Short: "Node agent: joins a hub and reports telemetry",
}

var (
	hubURL     string
	joinToken  string
	nodeName   string
	roles      []string
	listenAddr string
	sshAddr    string
)

var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join this machine to a hub using a join token from the UI",
	RunE: func(cmd *cobra.Command, args []string) error {
		hub := normalizeHub(hubURL)
		ts, err := tailnet(cmd.Context(), nodeName)
		if err != nil {
			return err
		}
		defer ts.Close() // `node run` comes back up with the same identity: same state dir
		body, _ := json.Marshal(map[string]any{"token": joinToken, "name": nodeName, "roles": roles})

		var out struct {
			Node  fleet.Node `json:"node"`
			Token string     `json:"token"`
		}
		if err := post(cmd.Context(), hub+"/api/nodes/join", "", body, &out); err != nil {
			return ui.WrapError("join %s: %w", hub, err)
		}

		st := state{Hub: hub, ID: out.Node.ID, Name: out.Node.Name, Token: out.Token}
		if err := saveState(st); err != nil {
			return ui.WrapError("save node state: %w", err)
		}

		ui.PrintSuccess("Joined %s as '%s' (%s).", hub, st.Name, strings.Join(out.Node.Roles, ", "))
		ui.PrintKeyValue("State", statePath())
		fmt.Println("\n  Start the agent:  walroos node run")
		fmt.Println("  Keep it running:  walroos node install   (systemd --user service)")
		return nil
	},
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the agent: heartbeat with telemetry every 15s",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := loadState()
		if err != nil {
			return ui.WrapError("no node state at %s, run `walroos node join` first: %w", statePath(), err)
		}

		ctx := cmd.Context()
		ts, err := tailnet(ctx, st.Name)
		if err != nil {
			return err
		}
		defer ts.Close()
		lc, err := ts.LocalClient()
		if err != nil {
			return ui.WrapError("tailscale local client: %w", err)
		}
		slog.Info("Node agent starting", "name", st.Name, "hub", st.Hub, "listen", listenAddr)

		// Only reachable over the tailnet (tsnet listener); anything not from the hub fails WhoIs.
		api := agent.NewServer(agent.Tailscale(lc), st.Token)
		if exe, err := os.Executable(); err == nil {
			api.SetRelay([]string{exe, "node", "relay"}) // app health checks run it in the app's netns
		}
		ln, err := ts.Listen("tcp", listenAddr)
		if err != nil {
			return ui.WrapError("agent listen %s: %w", listenAddr, err)
		}
		srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(ln)
		defer srv.Close()

		// Prod nodes pull builds from a build node's registry over the tailnet.
		api.SetDial(ts.Dial)
		regLn, err := ts.Listen("tcp", ":"+agent.RegistryPort)
		if err != nil {
			return ui.WrapError("registry listen: %w", err)
		}
		reg := &http.Server{Handler: api.RegistryHandler(), ReadHeaderTimeout: 10 * time.Second}
		go reg.Serve(regLn)
		defer reg.Close()

		if sshAddr != "" {
			sshLn, err := startSSH(ts, api, st)
			if err != nil {
				return err
			}
			defer sshLn.Close()
		}

		ticker := time.NewTicker(fleet.HeartbeatInterval)
		defer ticker.Stop()
		for {
			hubID, err := heartbeat(ctx, st)
			api.SetHubID(hubID)
			var he *httpError
			switch {
			case errors.As(err, &he) && he.status == http.StatusUnauthorized:
				return ui.WrapError("the hub rejected this node (removed?). Join again with a new token")
			case err != nil:
				slog.Error("Heartbeat failed", "hub", st.Hub, "err", err) // hub down or network blip: keep trying
			}

			select {
			case <-ctx.Done():
				slog.Info("Node agent stopped")
				return nil
			case <-ticker.C:
			}
		}
	},
}

func init() {
	host, _ := os.Hostname()
	joinCmd.Flags().StringVar(&hubURL, "hub", "walroos-hub", "Hub address on the tailnet (http:// is assumed)")
	joinCmd.Flags().StringVar(&joinToken, "token", "", "Join token from the hub UI (Nodes → Add node)")
	joinCmd.Flags().StringVar(&nodeName, "name", strings.ToLower(host), "Node name (DNS label)")
	joinCmd.Flags().StringSliceVar(&roles, "roles", []string{"dev"}, "Roles: dev, build, prod")
	joinCmd.MarkFlagRequired("token")
	runCmd.Flags().StringVar(&listenAddr, "listen", ":"+agent.Port, "Tailnet port for the agent API the hub calls")
	runCmd.Flags().StringVar(&sshAddr, "ssh-listen", ":"+agent.SSHPort, "Tailnet port for the workspaces' SSH gateway (empty disables it)")

	Cmd.AddCommand(joinCmd, runCmd, installCmd, relayCmd)
}

const serviceName = "walroos-node.service"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Run the agent as a systemd --user service that starts on boot",
	Long: "Writes ~/.config/systemd/user/" + serviceName + ", enables and starts it, and enables lingering\n" +
		"so it runs without a login session. Remove with: systemctl --user disable --now walroos-node",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := loadState(); err != nil {
			return ui.WrapError("no node state at %s, run `walroos node join` first: %w", statePath(), err)
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return err
		}
		cfg, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		path := filepath.Join(cfg, "systemd", "user", serviceName)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(unit(exe)), 0o644); err != nil {
			return err
		}
		ctx := cmd.Context()
		for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", serviceName}} {
			if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil {
				return ui.WrapError("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
		}
		ui.PrintSuccess("Installed and started %s.", serviceName)
		ui.PrintKeyValue("Unit", path)
		fmt.Println("\n  Logs:  journalctl --user -u walroos-node -f")

		// Without lingering, user services stop at logout and don't start on boot. loginctl can
		// exit 0 even when polkit says no (no active session), so read the setting back.
		u, err := user.Current()
		if err != nil {
			return err
		}
		exec.CommandContext(ctx, "loginctl", "enable-linger", u.Username).Run()
		if out, _ := exec.CommandContext(ctx, "loginctl", "show-user", u.Username, "-p", "Linger", "--value").Output(); strings.TrimSpace(string(out)) != "yes" {
			fmt.Printf("\n  To start on boot without logging in, run:  sudo loginctl enable-linger %s\n", u.Username)
		}
		return nil
	},
}

// unit is the systemd --user service for `node run`. on-failure restarts it after a crash or
// after the hub rejects the node (exit 1): once you `node join` again, it comes back on its own.
// KillMode=process: a workspace the agent starts (podman start/run) keeps its conmon in this
// service's cgroup, and the default control-group mode killed it on every restart, stopping the
// workspace. Only the agent itself is stopped now; containers and their conmon live on.
// ponytail: a clone or build in flight also outlives a restart, unwatched (the agent forgets it).
func unit(exe string) string {
	return `[Unit]
Description=walroos node agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart="` + exe + `" node run
Restart=on-failure
RestartSec=10
KillMode=process

[Install]
WantedBy=default.target
`
}

// relayCmd is how the SSH gateway reaches ports inside a workspace (ssh -L, VS Code): it runs
// in the workspace's network namespace, chrooted to ROOT (its filesystem, for name lookups),
// and pipes HOST:PORT to stdin/stdout.
var relayCmd = &cobra.Command{
	Use:    "relay ROOT HOST:PORT",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE:   func(cmd *cobra.Command, args []string) error { return agent.Relay(args[0], args[1]) },
}

// startSSH serves the SSH gateway. Keys are checked with the hub; the host key lives next to
// node.json so clients see the same one across restarts.
func startSSH(ts *tsnet.Server, api *agent.Server, st state) (net.Listener, error) {
	hostKey, err := agent.LoadOrCreateHostKey(filepath.Join(filepath.Dir(statePath()), "ssh_host_ed25519_key"))
	if err != nil {
		return nil, ui.WrapError("ssh host key: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, ui.WrapError("find walroos binary for port forwarding: %w", err)
	}
	authorize := func(ctx context.Context, workspace, fingerprint string) (string, error) {
		body, _ := json.Marshal(map[string]string{"workspace": workspace, "fingerprint": fingerprint})
		var out struct {
			Login string `json:"login"`
		}
		err := post(ctx, st.Hub+"/api/nodes/"+st.ID+"/ssh-auth", st.Token, body, &out)
		return out.Login, err
	}
	gw := api.SSHGateway(authorize, hostKey, []string{exe, "node", "relay"})

	ln, err := ts.Listen("tcp", sshAddr) // tailnet only, like the agent API
	if err != nil {
		return nil, ui.WrapError("ssh listen %s: %w", sshAddr, err)
	}
	go gw.Serve(ln)
	slog.Info("SSH gateway listening", "addr", sshAddr, "host_key", ssh.FingerprintSHA256(hostKey.PublicKey()))
	return ln, nil
}

// heartbeat reports telemetry and returns the hub's tailnet StableNodeID.
func heartbeat(ctx context.Context, st state) (string, error) {
	body, err := json.Marshal(collectTelemetry(ctx))
	if err != nil {
		return "", err
	}
	var out struct {
		HubID string `json:"hub_id"`
	}
	err = post(ctx, st.Hub+"/api/nodes/"+st.ID+"/heartbeat", st.Token, body, &out)
	return out.HubID, err
}

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d: %s", e.status, e.msg) }

var client = &http.Client{Timeout: 10 * time.Second} // tailnet() swaps in tsnet's client

// tailnet brings the node up on the tailnet as walroos-<name> with an embedded tsnet, like the
// hub: no tailscaled on the host, and it works behind host VPNs (see netns below). The first
// time it prints a login URL, or uses TS_AUTHKEY. State lives next to node.json.
// ponytail: userspace netstack, slower than a kernel TUN for big transfers over SSH; and the
// machine shows up twice on the tailnet if it also runs tailscaled.
func tailnet(ctx context.Context, name string) (*tsnet.Server, error) {
	// tsnet is userspace-only, so its sockets don't need to bypass the host's routing; bypassing
	// skips VPNs like NordVPN, whose firewall then drops the traffic. Same as the hub.
	netns.SetEnabled(false)
	// Not "tsnet": a hub on the same machine keeps its state there.
	ts := &tsnet.Server{
		Hostname: "walroos-" + name,
		Dir:      filepath.Join(filepath.Dir(statePath()), "tsnet-node"),
		Logf:     func(string, ...any) {}, // backend noise; UserLogf still prints the login URL
	}
	slog.Info("Connecting to the tailnet...", "hostname", ts.Hostname)
	status, err := ts.Up(ctx)
	if err != nil {
		ts.Close()
		return nil, ui.WrapError("tailscale up: %w", err)
	}
	slog.Info("On the tailnet", "dns", strings.TrimSuffix(status.Self.DNSName, "."), "ips", status.TailscaleIPs)
	client = ts.HTTPClient()
	client.Timeout = 10 * time.Second
	return ts, nil
}

func post(ctx context.Context, url, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return &httpError{status: resp.StatusCode, msg: e.Error}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent { // pre-M1 hubs answer heartbeats with 204
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func normalizeHub(h string) string {
	h = strings.TrimSuffix(h, "/")
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	return h
}

func statePath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "walroos", "node.json")
}

func saveState(st state) error {
	if err := os.MkdirAll(filepath.Dir(statePath()), 0o700); err != nil {
		return err
	}
	body, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(statePath(), body, 0o600) // holds the node token
}

func loadState() (state, error) {
	var st state
	body, err := os.ReadFile(statePath())
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(body, &st)
}
