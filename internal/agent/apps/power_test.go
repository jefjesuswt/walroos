package apps

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent"
)

// A stopped app's unit loses only its WantedBy (it doesn't start on boot), and gets it back.
func TestSetWanted(t *testing.T) {
	unit := []byte(appUnit(agent.AppSpec{Name: "web"}, agent.Release{Container: "web-1", Sha: "abc", Image: "img"}, "/env", "", ""))
	off := setWanted(unit, false)
	if bytes.Contains(off, []byte("\nWantedBy=")) || !strings.Contains(string(off), "# WantedBy=default.target") {
		t.Fatalf("stopped unit:\n%s", off)
	}
	if !bytes.Equal(setWanted(off, false), off) || !bytes.Equal(setWanted(off, true), unit) || !bytes.Equal(setWanted(unit, true), unit) {
		t.Fatal("setWanted isn't idempotent or doesn't restore the unit")
	}
}
