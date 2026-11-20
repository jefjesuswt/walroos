package sshconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

// Like the owner's real settings.json: comments, the same host added twice from Zed's UI, an
// unrelated server, a workspace that was deleted, and other settings around.
const zedSettings = `{
  // Zed settings
  "ssh_connections": [
    { "host": "nimbo-cli.walroos", "args": ["-v"], "projects": [] },
    { "host": "nimbo-cli.walroos", "args": [], "projects": [{ "paths": ["/tmp/scratch"] }] },
    { "host": "my-vps", "projects": [{ "paths": ["~/srv"] }] }, // not walroos
    { "host": "deleted.walroos", "projects": [] }
  ],
  "outline_panel": {
    "dock": "left", // trailing comma next
  },
}
`

func decodeZed(t *testing.T, b []byte) map[string]any {
	t.Helper()
	v, err := hujson.Parse(b)
	if err != nil {
		t.Fatalf("output isn't valid JSONC: %v\n%s", err, b)
	}
	v.Standardize()
	var doc map[string]any
	if err := json.Unmarshal(v.Pack(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMergeZed(t *testing.T) {
	list := []workspace{{Name: "nimbo-cli", Folder: "/workspaces/nimbo-cli"}, {Name: "gpay", Folder: "/workspaces/gPay.Net"}}
	out, changed, warnings, err := MergeZed([]byte(zedSettings), list, ".walroos")
	if err != nil || !changed || len(warnings) != 0 {
		t.Fatalf("merge: changed=%v warnings=%v err=%v", changed, warnings, err)
	}
	for _, keep := range []string{"// Zed settings", `"dock": "left", // trailing comma next`} {
		if !strings.Contains(string(out), keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}

	doc := decodeZed(t, out)
	conns := doc["ssh_connections"].([]any)
	byHost := map[string]map[string]any{}
	for _, c := range conns {
		m := c.(map[string]any)
		if byHost[m["host"].(string)] != nil {
			t.Errorf("host %s appears twice", m["host"])
		}
		byHost[m["host"].(string)] = m
	}
	if len(conns) != 3 || byHost["deleted.walroos"] != nil || byHost["my-vps"] == nil {
		t.Fatalf("connections: %v (want my-vps, gpay and nimbo-cli; deleted.walroos gone)", conns)
	}
	nimbo, _ := json.Marshal(byHost["nimbo-cli.walroos"])
	for _, want := range []string{`"args":["-v"]`, `"/tmp/scratch"`, `"/workspaces/nimbo-cli"`} {
		if !strings.Contains(string(nimbo), want) {
			t.Errorf("nimbo-cli entry %s lacks %s (duplicates merged, user's args and projects kept)", nimbo, want)
		}
	}
	if gpay, _ := json.Marshal(byHost["gpay.walroos"]); !strings.Contains(string(gpay), `"paths":["/workspaces/gPay.Net"]`) {
		t.Errorf("gpay entry: %s", gpay)
	}
	if vps, _ := json.Marshal(byHost["my-vps"]); string(vps) != `{"host":"my-vps","projects":[{"paths":["~/srv"]}]}` {
		t.Errorf("unrelated server changed: %s", vps)
	}

	// The list is written indented like the rest of the file, in place, and only once.
	if !strings.Contains(string(out), "\n  \"ssh_connections\": [\n    {\n") || strings.Count(string(out), `"ssh_connections"`) != 1 {
		t.Errorf("ssh_connections not replaced in place, indented:\n%s", out)
	}

	// Idempotent: a second run changes nothing.
	again, changed, _, err := MergeZed(out, list, ".walroos")
	if err != nil || changed || string(again) != string(out) {
		t.Errorf("second run: changed=%v err=%v", changed, err)
	}
}

func TestMergeZedSadPaths(t *testing.T) {
	ws := []workspace{{Name: "app", Folder: "/workspaces/app"}}

	// Missing or empty file: a settings object with just ssh_connections.
	out, changed, _, err := MergeZed(nil, ws, ".walroos")
	if err != nil || !changed || !strings.Contains(string(out), `"/workspaces/app"`) {
		t.Fatalf("empty settings: %v %v\n%s", changed, err, out)
	}

	// A settings object without ssh_connections gets it right after the brace, indented, and the
	// rest of the file stays byte for byte.
	mine := "{\n  // mine\n  \"theme\": \"One Dark\",\n}\n"
	out, changed, _, err = MergeZed([]byte(mine), ws, ".walroos")
	if err != nil || !changed || !strings.HasSuffix(string(out), "  // mine\n  \"theme\": \"One Dark\",\n}\n") ||
		!strings.HasPrefix(string(out), "{\n  \"ssh_connections\": [\n    {\n") {
		t.Errorf("insert into existing settings: %v\n%s", err, out)
	}
	decodeZed(t, out)

	// Node down: no folder. Keep what Zed had, warn, don't invent a path.
	had := `{"ssh_connections": [{"host": "app.walroos", "projects": [{"paths": ["/workspaces/app"]}]}]}`
	out, changed, warnings, err := MergeZed([]byte(had), []workspace{{Name: "app"}}, ".walroos")
	if err != nil || changed || len(warnings) != 1 || string(out) != had {
		t.Errorf("unknown folder: changed=%v warnings=%v err=%v\n%s", changed, warnings, err, out)
	}

	// Broken or unexpected files are refused, never rewritten.
	for name, bad := range map[string]string{
		"not JSON":                  `{"ssh_connections": [`,
		"not an object":             `[]`,
		"ssh_connections not array": `{"ssh_connections": {"host": "x"}}`,
	} {
		if _, _, _, err := MergeZed([]byte(bad), ws, ".walroos"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// No workspaces and nothing of ours: untouched.
	plain := "{\n  // mine\n  \"theme\": \"One Dark\"\n}\n"
	if out, changed, _, err := MergeZed([]byte(plain), nil, ".walroos"); err != nil || changed || string(out) != plain {
		t.Errorf("nothing to do: changed=%v err=%v", changed, err)
	}
}

func TestWriteZedThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "settings.json")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte(zedSettings), 0o600)
	link := filepath.Join(dir, "zed", "settings.json")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}

	changed, _, err := writeZed(link, []workspace{{Name: "nimbo-cli", Folder: "/workspaces/nimbo-cli"}}, ".walroos")
	if err != nil || !changed {
		t.Fatalf("write: %v %v", changed, err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file (dotfiles would lose the change)")
	}
	b, _ := os.ReadFile(real)
	if !strings.Contains(string(b), "/workspaces/nimbo-cli") {
		t.Errorf("target not updated:\n%s", b)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want the original 0600", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(real), ".settings-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}

	// A broken file is left exactly as it was.
	os.WriteFile(real, []byte(`{"ssh_connections": [`), 0o600)
	if _, _, err := writeZed(link, []workspace{{Name: "x", Folder: "/workspaces/x"}}, ".walroos"); err == nil {
		t.Fatal("broken settings accepted")
	}
	if b, _ := os.ReadFile(real); string(b) != `{"ssh_connections": [` {
		t.Errorf("broken settings were rewritten: %s", b)
	}
}
