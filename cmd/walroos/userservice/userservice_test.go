package userservice

import (
	"strings"
	"testing"
)

func TestUnitText(t *testing.T) {
	u := Unit{Name: "walroos-hub.service", Description: "walroos hub", Args: []string{"hub", "--data", "/home/me/my data", "--owner", "100%$x"}}
	text := u.Text("/opt/walroos/bin/walroos")
	if !strings.Contains(text, `ExecStart="/opt/walroos/bin/walroos" "hub" "--data" "/home/me/my data" "--owner" "100%%$$x"`+"\n") {
		t.Errorf("ExecStart not quoted for systemd:\n%s", text)
	}
	if strings.Contains(text, "KillMode") {
		t.Error("KillMode=process without asking")
	}
	if !strings.Contains(Unit{KillProcess: true}.Text("/w"), "KillMode=process\n") {
		t.Error("KillProcess doesn't write KillMode=process")
	}
}
