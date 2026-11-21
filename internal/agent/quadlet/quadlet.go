// Package quadlet runs long-lived containers (app releases, databases, cloudflared) as Podman
// Quadlet units under systemd --user: it owns where the units go, the per-name lock that keeps
// two operations off one unit, the egress guard and the project networks.
package quadlet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

const (
	LabelApp     = "walroos.app"
	LabelSha     = "walroos.sha"
	LabelDB      = "walroos.db"
	LabelProject = "walroos.project"
	EdgeNetwork  = "walroos-edge" // cloudflared and the releases it routes to
)

var ErrBusy = errors.New("a deploy or delete of this app is already running on this node")

// Host is this node's unit directory and data root, shared by apps, databases and the tunnel.
type Host struct {
	Dir  string // where the Quadlet files go; systemd's generator reads it
	Data string // data root: volumes' HostPath lives under it

	mu   sync.Mutex
	busy map[string]bool
}

func New() *Host {
	cfg, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	// ponytail: fixed data root; make it a symlink to put app data on another disk.
	return &Host{busy: map[string]bool{}, Dir: filepath.Join(cfg, "containers", "systemd"), Data: filepath.Join(home, "walroos-data")}
}

// remove stops a release and deletes its unit and env file. The image stays, for rollback.
func (h *Host) Remove(ctx context.Context, container string) {
	host.Run(ctx, nil, "systemctl", "--user", "stop", container+".service")
	os.Remove(filepath.Join(h.Dir, container+".container"))
	os.Remove(filepath.Join(h.Dir, container+".env"))
	host.Run(ctx, nil, "systemctl", "--user", "daemon-reload")
	host.Run(ctx, nil, "systemctl", "--user", "reset-failed", container+".service")
	host.Podman(ctx, "rm", "-f", "--ignore", container)
}

// lock marks app busy: one deploy or delete at a time per app.
func (h *Host) Lock(app string) (unlock func(), err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy[app] {
		return nil, ErrBusy
	}
	h.busy[app] = true
	return func() { h.mu.Lock(); delete(h.busy, app); h.mu.Unlock() }, nil
}

func UnitValue(unit []byte, prefix string) string {
	for _, l := range strings.Split(string(unit), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), prefix); ok {
			return v
		}
	}
	return ""
}

// ensureServiceNetwork creates a project's network the first time: labeled (the egress guard
// finds it by the label) and isolate=strict, so no other network's containers can reach it.
func EnsureNetwork(ctx context.Context, n string) error {
	if n == "" || n == agent.AppNetwork {
		return host.EnsureNetwork(ctx, agent.AppNetwork)
	}
	if !agent.ValidNetwork(n) {
		return fmt.Errorf("bad network name %q", n)
	}
	if _, err := host.Podman(ctx, "network", "exists", n); err == nil {
		return nil
	}
	_, err := host.Podman(ctx, "network", "create", "--label", LabelProject+"="+strings.TrimPrefix(n, "walroos-p-"), "--opt", "isolate=strict", n)
	return err
}
