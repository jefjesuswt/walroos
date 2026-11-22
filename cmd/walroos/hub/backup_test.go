package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/auth"
)

// newHubDir makes a data dir like a hub's: a migrated hub.db with an owner, secret.key, tsnet/.
func newHubDir(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	data := t.TempDir()
	db, err := config.NewSQLite(filepath.Join(data, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewStore(db).EnsureOwner(ctx, "owner@github"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"secret.key": "k3y", "tsnet/tailscaled.state": "{\"node\":1}", "tsnet/certs/x.crt": "cert"} {
		p := filepath.Join(data, name)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(body), 0o600)
	}
	return data
}

func TestBackupRestore(t *testing.T) {
	src := newHubDir(t)
	archive := filepath.Join(t.TempDir(), "hub.tar.gz")
	if err := backup(context.Background(), src, archive, "my-hub"); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "walroos")
	m, err := restore(archive, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Hostname != "my-hub" || m.CreatedAt.IsZero() {
		t.Errorf("manifest = %+v", m)
	}
	for name, want := range map[string]string{"secret.key": "k3y", "tsnet/tailscaled.state": "{\"node\":1}", "tsnet/certs/x.crt": "cert"} {
		if b, _ := os.ReadFile(filepath.Join(dst, name)); string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
	db, err := config.NewSQLite(filepath.Join(dst, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := auth.NewStore(db).Count(context.Background())
	db.Close()
	if err != nil || n != 1 {
		t.Errorf("restored hub.db: %d users, %v", n, err)
	}
	if st, _ := os.Stat(filepath.Join(dst, "secret.key")); st.Mode().Perm() != 0o600 {
		t.Errorf("secret.key mode %v", st.Mode().Perm())
	}

	// A hub is there now: refused, unless --force, which keeps the old one aside.
	if _, err := restore(archive, dst, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("restore over a hub: %v", err)
	}
	if _, err := restore(archive, dst, true); err != nil {
		t.Fatal(err)
	}
	aside, _ := filepath.Glob(dst + ".before-restore-*")
	if len(aside) != 1 {
		t.Errorf("the old hub wasn't kept aside: %v", aside)
	}
}

func TestRestoreRefusesForeignArchives(t *testing.T) {
	archive := func(files map[string]string) *bytes.Buffer {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
			tw.Write([]byte(body))
		}
		tw.Close()
		gz.Close()
		return &buf
	}
	ok := map[string]string{manifestName: "{}", "hub.db": "x", "secret.key": "k"}
	for name, files := range map[string]map[string]string{
		"path traversal": {manifestName: "{}", "hub.db": "x", "secret.key": "k", "../../.bashrc": "evil"},
		"absolute path":  {manifestName: "{}", "hub.db": "x", "secret.key": "k", "/etc/x": "evil"},
		"other file":     {manifestName: "{}", "hub.db": "x", "secret.key": "k", "node.json": "x"},
		"no secret.key":  {manifestName: "{}", "hub.db": "x"},
	} {
		if _, err := extract(archive(files), t.TempDir()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := extract(archive(ok), t.TempDir()); err != nil {
		t.Errorf("a minimal backup: %v", err)
	}
	if _, err := extract(strings.NewReader("not gzip"), t.TempDir()); err == nil {
		t.Error("accepted a file that isn't a backup")
	}
}
