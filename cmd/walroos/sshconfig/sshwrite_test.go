package sshconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Like a real ~/.ssh/config after `walroos ssh-config >> ~/.ssh/config` ran twice: the user's own
// blocks, the same workspace block pasted twice, a comment about the next block, and a wildcard
// block for all workspaces that the user wrote.
const pasted = `Host *
    AddKeysToAgent yes

Host app.walroos
  HostName 100.64.0.1
  Port 2222
  User app

# my git key
Host github.com
    IdentityFile ~/.ssh/id_git

Host app.walroos old.walroos
  HostName 100.64.0.1
  Port 2222

Host *.walroos
  ForwardAgent no
`

var twoHosts = managedBlock(hostBlocks([]workspace{
	{Name: "app", NodeAddress: "100.64.0.2"}, {Name: "api", NodeAddress: "100.64.0.3"},
}, "localhost:1055", 2222, ".walroos"))

func TestMergeSSH(t *testing.T) {
	out, changed, dropped, err := MergeSSH([]byte(pasted), twoHosts, ".walroos")
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}
	got := string(out)
	want := `Host *
    AddKeysToAgent yes

# my git key
Host github.com
    IdentityFile ~/.ssh/id_git

Host *.walroos
  ForwardAgent no

` + twoHosts
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Join(dropped, "|") != "app.walroos|app.walroos old.walroos" {
		t.Errorf("dropped: %q", dropped)
	}
	if !strings.Contains(twoHosts, "Host api.walroos\n  HostName 100.64.0.3\n  Port 2222\n  User api\n  ProxyCommand nc -X 5 -x localhost:1055 %h %p\n") {
		t.Errorf("block:\n%s", twoHosts)
	}

	// Idempotent.
	again, changed, _, err := MergeSSH(out, twoHosts, ".walroos")
	if err != nil || changed || string(again) != got {
		t.Errorf("second run: changed=%v err=%v\n%s", changed, err, again)
	}

	// A later run replaces the section where it is; text after it stays after it.
	moved := strings.Replace(got, twoHosts, twoHosts+"\nHost later\n  User me\n", 1)
	one := managedBlock(hostBlocks([]workspace{{Name: "app", NodeAddress: "100.64.0.9"}}, "", 2222, ".walroos"))
	out, changed, _, err = MergeSSH([]byte(moved), one, ".walroos")
	if err != nil || !changed || !strings.Contains(string(out), one+"\nHost later\n") || strings.Contains(string(out), "api.walroos") {
		t.Errorf("replace in place: %v\n%s", err, out)
	}

	// No workspaces left: the section goes away, and the rest stays.
	out, changed, _, err = MergeSSH([]byte(moved), managedBlock(""), ".walroos")
	if err != nil || !changed || strings.Contains(string(out), "walroos section") || strings.Contains(string(out), beginMarker) ||
		!strings.Contains(string(out), "Host later\n") {
		t.Errorf("empty: %v\n%s", err, out)
	}
}

func TestMergeSSHSadPaths(t *testing.T) {
	// A missing file gets just the section; one without a final newline gets one first.
	out, changed, _, err := MergeSSH(nil, twoHosts, ".walroos")
	if err != nil || !changed || string(out) != twoHosts {
		t.Errorf("empty file: %v\n%s", err, out)
	}
	out, _, _, _ = MergeSSH([]byte("Host x\n  User me"), twoHosts, ".walroos")
	if string(out) != "Host x\n  User me\n\n"+twoHosts {
		t.Errorf("no final newline:\n%s", out)
	}
	// Nothing to write and nothing of ours: untouched.
	if out, changed, _, _ := MergeSSH([]byte("Host x\n"), "", ".walroos"); changed || string(out) != "Host x\n" {
		t.Errorf("nothing to do changed the file:\n%s", out)
	}

	// Host=, Match blocks and lookalikes: only plain *.walroos names are ours.
	cfg := "Host=a.walroos\n  User a\nMatch host b.walroos\n  User b\nHost walroos.example.com c.walroos\n  User c\nHost !d.walroos\n  User d\n"
	out, _, dropped, _ := MergeSSH([]byte(cfg), "", ".walroos")
	if string(out) != strings.TrimPrefix(cfg, "Host=a.walroos\n  User a\n") || len(dropped) != 1 {
		t.Errorf("only Host=a.walroos should go: dropped=%q\n%s", dropped, out)
	}

	// Markers out of order: refused, so a hand-edited file is never mangled.
	for name, bad := range map[string]string{
		"BEGIN without END": "Host x\n" + beginMarker + "\nHost a.walroos\n",
		"END without BEGIN": "Host x\n" + endMarker + "\n",
		"two sections":      twoHosts + twoHosts,
	} {
		if _, _, _, err := MergeSSH([]byte(bad), twoHosts, ".walroos"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWriteSSHThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "config")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.WriteFile(real, []byte(pasted), 0o600)
	link := filepath.Join(dir, ".ssh", "config")
	os.MkdirAll(filepath.Dir(link), 0o700)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}

	if changed, _, err := writeSSH(link, twoHosts, ".walroos"); err != nil || !changed {
		t.Fatalf("write: %v %v", changed, err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if b, _ := os.ReadFile(real); !strings.HasSuffix(string(b), twoHosts) {
		t.Errorf("target not updated:\n%s", b)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if changed, _, err := writeSSH(link, twoHosts, ".walroos"); err != nil || changed {
		t.Errorf("second write: changed=%v err=%v", changed, err)
	}

	// A new file is 0600 in a new 0700 ~/.ssh.
	fresh := filepath.Join(dir, "home", ".ssh", "config")
	if _, _, err := writeSSH(fresh, twoHosts, ".walroos"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(fresh)); fi.Mode().Perm() != 0o700 {
		t.Errorf("new dir mode %v", fi.Mode().Perm())
	}

	// A file it refuses stays as it was.
	broken := "Host x\n" + beginMarker + "\n"
	os.WriteFile(real, []byte(broken), 0o600)
	if _, _, err := writeSSH(link, twoHosts, ".walroos"); err == nil {
		t.Fatal("broken config accepted")
	}
	if b, _ := os.ReadFile(real); string(b) != broken {
		t.Errorf("broken config rewritten:\n%s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(real), ".config-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// TestFetchFallsBackToProxy: on a machine with userspace tailscaled the hub's tailnet address has
// no route, but tailscaled's proxy on localhost:1055 reaches it. The fake proxy stands in for it;
// the .invalid hub name never resolves, like a tailnet name without MagicDNS.
func TestFetchFallsBackToProxy(t *testing.T) {
	const hubURL = "http://walroos-hub.invalid"
	status := http.StatusOK
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "walroos-hub.invalid" || r.URL.Path != "/api/workspaces" {
			http.Error(w, "unexpected "+r.URL.String(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			json.NewEncoder(w).Encode([]map[string]string{{"name": "app", "node_address": "100.64.0.2"}})
		} else {
			w.Write([]byte(`{"error":"stranger@github is not allowed on this hub"}`))
		}
	}))
	t.Cleanup(proxySrv.Close)
	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")
	saved := defaultProxy
	t.Cleanup(func() { defaultProxy = saved })
	ctx := context.Background()

	// Direct fails, the proxy answers: the blocks go through it.
	defaultProxy = proxyAddr
	out, err := Generate(ctx, hubURL, "", 2222, ".walroos")
	if err != nil || !strings.Contains(out, "  ProxyCommand nc -X 5 -x "+proxyAddr+" %h %p\n") {
		t.Fatalf("fallback: %v\n%s", err, out)
	}

	// Through the proxy the hub says no: that's the hub's answer, not "unreachable".
	status = http.StatusForbidden
	if _, err := Generate(ctx, hubURL, "", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "doesn't know you") {
		t.Errorf("403 through the proxy: %v", err)
	}

	// The proxy finds nothing at that name (502): unreachable, with the hint.
	status = http.StatusBadGateway
	if _, err := Generate(ctx, hubURL, "", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "--proxy") {
		t.Errorf("502: %v", err)
	}

	// No proxy listening either: the hint, not a hang.
	defaultProxy = "127.0.0.1:1"
	if _, err := Generate(ctx, hubURL, "", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "--proxy") {
		t.Errorf("no proxy: %v", err)
	}

	// An explicit --proxy that's down says so, and doesn't try anything else.
	if _, err := Generate(ctx, hubURL, "127.0.0.1:1", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "through 127.0.0.1:1") {
		t.Errorf("explicit proxy down: %v", err)
	}

	// A direct hub that answers never goes through the proxy, and gets no ProxyCommand.
	status = http.StatusOK
	defaultProxy = proxyAddr
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"name": "app", "node_address": "100.64.0.2"}})
	}))
	t.Cleanup(direct.Close)
	if out, err := Generate(ctx, direct.URL, "", 2222, ".walroos"); err != nil || strings.Contains(out, "ProxyCommand") {
		t.Errorf("direct: %v\n%s", err, out)
	}
}
