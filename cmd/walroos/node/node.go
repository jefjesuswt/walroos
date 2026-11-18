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
	"path/filepath"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"tailscale.com/client/local"
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
		fmt.Println("  Keep it running:  systemd --user unit + `loginctl enable-linger $USER`")
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
		slog.Info("Node agent starting", "name", st.Name, "hub", st.Hub, "listen", listenAddr)

		// ponytail: listens on all interfaces because in userspace mode tailnet traffic arrives on
		// loopback; anything not from the hub fails WhoIs and is rejected.
		api := agent.NewServer(agent.Tailscale(&local.Client{}), st.Token)
		ln, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return ui.WrapError("agent listen %s: %w", listenAddr, err)
		}
		srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(ln)
		defer srv.Close()

		if sshAddr != "" {
			sshLn, err := startSSH(api, st)
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
	runCmd.Flags().StringVar(&listenAddr, "listen", ":"+agent.Port, "Address for the agent API the hub calls")
	runCmd.Flags().StringVar(&sshAddr, "ssh-listen", ":"+agent.SSHPort, "Address for the workspaces' SSH gateway (empty disables it)")

	Cmd.AddCommand(joinCmd, runCmd, relayCmd)
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
func startSSH(api *agent.Server, st state) (net.Listener, error) {
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

	// ponytail: all interfaces, like the agent API; connections that aren't from the tailnet
	// are dropped before the SSH handshake.
	ln, err := net.Listen("tcp", sshAddr)
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

var client = &http.Client{Timeout: 10 * time.Second}

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
