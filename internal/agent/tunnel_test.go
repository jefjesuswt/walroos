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

func TestGuardExecFailsClosed(t *testing.T) {
	line, err := guardExec("/home/u/.config/containers/systemd/walroos-guard.nft", "hello-0123abcd")
	if err != nil {
		t.Fatal(err)
	}
	want := `ExecStartPost=/bin/sh -c 'podman unshare --rootless-netns nft -f "$$0" || { podman kill --signal KILL "$$1"; exit 1; }' /home/u/.config/containers/systemd/walroos-guard.nft hello-0123abcd`
	if line != want {
		t.Errorf("got  %s\nwant %s", line, want)
	}
	for _, bad := range [][2]string{
		{"/home/my user/g.nft", "c"}, // space splits the argument
		{"/home/u%n/g.nft", "c"},     // systemd specifier
		{"/home/$HOME/g.nft", "c"},   // systemd variable
		{`/home/"u"/g.nft`, "c"},     // quotes
		{"/x/g.nft", "c; reboot"},    // container name
		{"/x/g.nft", ""},
	} {
		if _, err := guardExec(bad[0], bad[1]); err == nil {
			t.Errorf("guardExec(%q, %q) was accepted", bad[0], bad[1])
		}
	}
}
