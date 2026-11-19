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
	unit := tunnelUnit("/x/walroos-cloudflared.env")
	if !strings.Contains(unit, "\nNetwork="+EdgeNetwork+"\n") || !strings.Contains(unit, "\nEnvironmentFile=/x/walroos-cloudflared.env\n") {
		t.Errorf("unit:\n%s", unit)
	}
}
