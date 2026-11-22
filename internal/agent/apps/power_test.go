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

func TestExecLine(t *testing.T) {
	unit := appUnit(agent.AppSpec{Name: "w", Command: []string{"worker", "--queue", "$jobs"}}, agent.Release{Container: "w-1", Image: "img"}, "/env", "", "")
	if !strings.Contains(unit, "PidsLimit=2048\nExec=\"worker\" \"--queue\" \"$$jobs\"\n") {
		t.Fatalf("unit:\n%s", unit)
	}
	if strings.Contains(appUnit(agent.AppSpec{Name: "w"}, agent.Release{Container: "w-1", Image: "img"}, "/env", "", ""), "Exec=") {
		t.Fatal("no command, but an Exec= line")
	}
}

func TestAliasLine(t *testing.T) {
	unit := appUnit(agent.AppSpec{Name: "shop-db", Alias: "db", Network: "walroos-p-shop"}, agent.Release{Container: "shop-db-1", Image: "img"}, "/env", "", "")
	if !strings.Contains(unit, "\nNetwork=walroos-p-shop:alias=shop-db,alias=db\n") {
		t.Fatalf("unit:\n%s", unit)
	}
}
