package quadlet

import (
	"strings"
	"testing"
)

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

func TestGuardRules(t *testing.T) {
	if r := guardRules(`"podman1", "podman4"`); !strings.Contains(r, `iifname { "podman1", "podman4" } oifname != { "podman1", "podman4" } ip daddr { 10.0.0.0/8,`) || !strings.Contains(r, "delete table inet walroos_guard") {
		t.Errorf("guard rules:\n%s", r)
	}
}
