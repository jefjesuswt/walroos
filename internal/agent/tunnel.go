package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Apps reach the internet through a Cloudflare Tunnel: one cloudflared per prod node, on
// EdgeNetwork. A release joins that network with its app's name as alias only once healthy,
// so the tunnel's rules (hostname → http://<app>:<port>) never change on a deploy.
const (
	EdgeNetwork      = "walroos-edge"
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
	if err := ensureNetwork(ctx, EdgeNetwork); err != nil {
		return err
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return err
	}
	envFile := filepath.Join(a.dir, tunnelContainer+".env")
	unit := tunnelUnit(envFile)
	env := "TUNNEL_TOKEN=" + token + "\n" // never in argv: `podman inspect` and ps would show it
	unitFile := filepath.Join(a.dir, tunnelContainer+".container")
	oldUnit, _ := os.ReadFile(unitFile)
	oldEnv, _ := os.ReadFile(envFile)
	state, _ := podman(ctx, "inspect", "--format", "{{.State.Running}}", tunnelContainer)
	if string(oldUnit) == unit && string(oldEnv) == env && state == "true" {
		return nil
	}
	if err := os.WriteFile(envFile, []byte(env), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(unitFile, []byte(unit), 0o644); err != nil {
		return err
	}
	if err := run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return run(ctx, nil, "systemctl", "--user", "restart", tunnelContainer+".service")
}

func tunnelUnit(envFile string) string {
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

[Install]
WantedBy=default.target
`, cloudflaredImage, tunnelContainer, EdgeNetwork, envFile)
}

// expose puts a healthy release on EdgeNetwork under the app's name: now, and in its unit for
// the next start. Until the old release stops, the alias resolves to both; both are healthy.
func (a *Apps) expose(ctx context.Context, app, container string) error {
	if err := ensureNetwork(ctx, EdgeNetwork); err != nil {
		return err
	}
	if _, err := podman(ctx, "network", "connect", "--alias", app, EdgeNetwork, container); err != nil {
		return err
	}
	f := filepath.Join(a.dir, container+".container")
	unit, err := os.ReadFile(f)
	if err != nil {
		return err
	}
	line := []byte("Network=" + AppNetwork + "\n")
	if !bytes.Contains(unit, line) {
		return fmt.Errorf("%s: no %q line", f, strings.TrimSpace(string(line)))
	}
	unit = bytes.Replace(unit, line, append(line, "Network="+EdgeNetwork+":alias="+app+"\n"...), 1)
	if err := os.WriteFile(f, unit, 0o644); err != nil {
		return err
	}
	return run(ctx, nil, "systemctl", "--user", "daemon-reload") // no restart: it's serving
}
