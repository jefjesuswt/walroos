package agent

import (
	"context"
	"strings"
	"testing"
)

func TestTunnelToken(t *testing.T) {
	a := &Apps{dir: t.TempDir()}
	for _, bad := range []string{"", "short", "eyJhIjoiYWNjMSIsInQiOiJ0dW4xIn0=\nX=1", "eyJhIjoiYWNjMSIsInQiOiJ0dW4xIn0= x", strings.Repeat("a", 5000)} {
		if err := a.SetTunnel(context.Background(), bad); err != ErrInvalidTunnel { // checked before podman or systemd
			t.Errorf("%q: %v", bad, err)
		}
	}
	unit := tunnelUnit("/x/walroos-cloudflared.env", "ExecStartPost=guard")
	if !strings.Contains(unit, "\nNetwork="+EdgeNetwork+"\n") || !strings.Contains(unit, "\nEnvironmentFile=/x/walroos-cloudflared.env\n") || !strings.Contains(unit, "\nExecStartPost=guard\n") {
		t.Errorf("unit:\n%s", unit)
	}
	if r := guardRules(`"podman1", "podman4"`); !strings.Contains(r, `iifname { "podman1", "podman4" } oifname != { "podman1", "podman4" } ip daddr { 10.0.0.0/8,`) || !strings.Contains(r, "delete table inet walroos_guard") {
		t.Errorf("guard rules:\n%s", r)
	}
}
