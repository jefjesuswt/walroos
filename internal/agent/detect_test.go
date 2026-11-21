package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent/agenttest"
)

func TestDetectNotes(t *testing.T) {
	dir := t.TempDir()
	for p, body := range map[string]string{"global.json": "{}", "web/package.json": "{}", "compose.yml": ""} {
		agenttest.WriteFile(t, filepath.Join(dir, p), body)
	}
	notes := strings.Join(DetectDir(dir).Notes, "\n")
	if !strings.Contains(notes, "web/package.json") || !strings.Contains(notes, DevNetwork) {
		t.Errorf("notes = %q, want the second stack and the compose hint", notes)
	}
}

// A repo's go.mod -> /dev/zero (or a FIFO) must not be read: it would eat the agent's memory.
func TestDetectSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(os.DevNull, filepath.Join(dir, "go.mod")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644)
	if d := DetectDir(dir); d.Stack != "node" {
		t.Errorf("stack = %q, want node (the go.mod symlink ignored)", d.Stack)
	}
}
