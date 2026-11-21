package apps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/jefjesuswt/walroos/internal/agent/quadlet"

	"github.com/jefjesuswt/walroos/internal/agent/host"
)

// Apps reach the internet through a Cloudflare Tunnel: one cloudflared per prod node, on
// quadlet.EdgeNetwork. A release joins that network with its app's name as alias only once healthy,
// so the tunnel's rules (hostname → http://<app>:<port>) never change on a deploy.
const (
	tunnelContainer  = "walroos-cloudflared"
	cloudflaredImage = "docker.io/cloudflare/cloudflared:latest" // ponytail: latest; pin when an update breaks it
)

var ErrInvalidTunnel = errors.New("invalid tunnel token")

var tunnelTokenRe = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{20,}$`) // goes into an env file

// SetTunnel runs cloudflared for the tunnel token, restarting it only when the token changed.
func (a *Apps) SetTunnel(ctx context.Context, token string) error {
	if len(token) > 4096 || !tunnelTokenRe.MatchString(token) {
		return ErrInvalidTunnel
	}
	envFile := filepath.Join(a.q.Dir, tunnelContainer+".env")
	guard, err := a.q.Guard(ctx, tunnelContainer)
	if err != nil {
		return err
	}
	unit := tunnelUnit(envFile, guard)
	env := "TUNNEL_TOKEN=" + token + "\n" // never in argv: `podman inspect` and ps would show it
	unitFile := filepath.Join(a.q.Dir, tunnelContainer+".container")
	oldUnit, _ := os.ReadFile(unitFile)
	oldEnv, _ := os.ReadFile(envFile)
	state, _ := host.Podman(ctx, "inspect", "--format", "{{.State.Running}}", tunnelContainer)
	if string(oldUnit) == unit && string(oldEnv) == env && state == "true" {
		return nil
	}
	if err := os.WriteFile(envFile, []byte(env), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(unitFile, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := host.Run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return host.Run(ctx, nil, "systemctl", "--user", "restart", tunnelContainer+".service")
}

func tunnelUnit(envFile, guard string) string {
	return fmt.Sprintf(`# Written by walroos; the hub sets the token. Don't edit.
[Unit]
Description=walroos Cloudflare Tunnel

[Container]
Image=%s
ContainerName=%s
Network=%s
EnvironmentFile=%s
Exec=tunnel --no-autoupdate run
NoNewPrivileges=true

[Quadlet]
DefaultDependencies=false

[Service]
Restart=always
# Without a delay five quick failures (no network at boot) hit systemd's start limit and the
# tunnel stays down for good.
RestartSec=10
%s

[Install]
WantedBy=default.target
`, cloudflaredImage, tunnelContainer, quadlet.EdgeNetwork, envFile, guard)
}

// expose puts a healthy release on quadlet.EdgeNetwork under the app's name: now, and in its unit for
// the next start. Until the old release stops, the alias resolves to both; both are healthy.
func (a *Apps) expose(ctx context.Context, app, container string) error {
	if err := host.EnsureNetwork(ctx, quadlet.EdgeNetwork); err != nil {
		return err
	}
	if _, err := host.Podman(ctx, "network", "connect", "--alias", app, quadlet.EdgeNetwork, container); err != nil {
		return err
	}
	f := filepath.Join(a.q.Dir, container+".container")
	unit, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	// After the unit's own network line (AppNetwork or the project's).
	i := bytes.Index(unit, []byte("\nNetwork="))
	if i < 0 {
		return fmt.Errorf("%s: no Network= line", f)
	}
	end := i + 1 + bytes.IndexByte(unit[i+1:], '\n') + 1
	unit = append(unit[:end:end], append([]byte("Network="+quadlet.EdgeNetwork+":alias="+app+"\n"), unit[end:]...)...)
	if err := os.WriteFile(f, unit, 0o644); err != nil {
		return err
	}
	return host.Run(ctx, nil, "systemctl", "--user", "daemon-reload") // no restart: it's serving
}
