package apps

import (
	"context"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
)

func TestTunnelToken(t *testing.T) {
	a := New(&quadlet.Host{Dir: t.TempDir()})
	for _, bad := range []string{"", "short", "eyJhIjoiYWNjMSIsInQiOiJ0dW4xIn0=\nX=1", "eyJhIjoiYWNjMSIsInQiOiJ0dW4xIn0= x", strings.Repeat("a", 5000)} {
		if err := a.SetTunnel(context.Background(), bad); err != ErrInvalidTunnel { // checked before podman or systemd
			t.Errorf("%q: %v", bad, err)
		}
	}
	unit := tunnelUnit("/x/walroos-cloudflared.env", "ExecStartPost=guard")
	if !strings.Contains(unit, "\nNetwork="+quadlet.EdgeNetwork+"\n") || !strings.Contains(unit, "\nEnvironmentFile=/x/walroos-cloudflared.env\n") || !strings.Contains(unit, "\nExecStartPost=guard\n") {
		t.Errorf("unit:\n%s", unit)
	}
}
