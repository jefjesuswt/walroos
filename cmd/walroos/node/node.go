package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
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
	hubURL    string
	joinToken string
	nodeName  string
	roles     []string
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
		slog.Info("Node agent starting", "name", st.Name, "hub", st.Hub)

		ticker := time.NewTicker(fleet.HeartbeatInterval)
		defer ticker.Stop()
		for {
			err := heartbeat(ctx, st)
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

	Cmd.AddCommand(joinCmd, runCmd)
}

func heartbeat(ctx context.Context, st state) error {
	body, err := json.Marshal(collectTelemetry(ctx))
	if err != nil {
		return err
	}
	return post(ctx, st.Hub+"/api/nodes/"+st.ID+"/heartbeat", st.Token, body, nil)
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
	if out != nil {
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
