package hub

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/cmd/walroos/userservice"
	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
)

// A backup is everything the hub needs to come back as itself: hub.db (a consistent copy),
// secret.key (without it the sealed tokens and variables can't be opened) and tsnet/ (its
// identity and address on the tailnet, so nodes find it again). They go together: a hub.db
// without its secret.key loses every secret.
const manifestName = "walroos-backup.json"

type manifest struct {
	Version   string    `json:"version"`
	Hostname  string    `json:"hostname,omitempty"` // from --hostname, when given
	CreatedAt time.Time `json:"created_at"`
}

var backupCmd = &cobra.Command{
	Use:   "backup [FILE]",
	Short: "Save hub.db, secret.key and the tailnet identity to a .tar.gz",
	Long: `Writes a consistent copy of the hub's database, its secret key and its tailnet state to FILE
(default walroos-hub-<date>.tar.gz). It works while the hub runs. Keep it somewhere safe: it
holds the key that opens every token and variable the hub keeps.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := "walroos-hub-" + time.Now().Format("20060102-150405") + ".tar.gz"
		if len(args) == 1 {
			out = args[0]
		}
		name := "" // the flag's default isn't necessarily the hub's name
		if cmd.Flags().Changed("hostname") {
			name = hostname
		}
		if err := backup(cmd.Context(), dataDir, out, name); err != nil {
			return ui.WrapError("backup: %w", err)
		}
		ui.PrintSuccess("Saved %s", out)
		fmt.Println("  It holds secret.key: store it like a password.")
		return nil
	},
}

var restoreForce bool

var restoreCmd = &cobra.Command{
	Use:   "restore FILE",
	Short: "Bring a hub back from a backup, on this machine or another one",
	Long: `Puts hub.db, secret.key and the tailnet state from FILE into --data. The hub must be stopped
(systemctl --user stop walroos-hub). With --force an existing hub in --data is moved aside to
<data>.before-restore-<date> instead of refusing. Never run two hubs with the same tailnet state.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(filepath.Join(dataDir, "hub.db")); err == nil && userservice.Active(cmd.Context(), serviceName) {
			return ui.WrapError("the hub in %s is running: stop it first with `systemctl --user stop walroos-hub`", dataDir)
		}
		m, err := restore(args[0], dataDir, restoreForce)
		if err != nil {
			return ui.WrapError("restore: %w", err)
		}
		ui.PrintSuccess("Restored the hub (backup of %s, walroos %s) into %s", m.CreatedAt.Format(time.DateTime), m.Version, dataDir)
		fmt.Println("  Start it with: walroos hub   (or systemctl --user start walroos-hub)")
		return nil
	},
}

func init() {
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "Move an existing hub in --data aside instead of refusing")
}

// backup writes the archive to out through a temp file, so a failed backup leaves nothing.
func backup(ctx context.Context, data, out, name string) error {
	snap, err := snapshot(ctx, filepath.Join(data, "hub.db"))
	if err != nil {
		return err
	}
	defer os.Remove(snap)
	tmp, err := os.CreateTemp(filepath.Dir(out), ".walroos-backup-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if err := writeArchive(tmp, data, snap, name); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}

// snapshot copies the database with VACUUM INTO: consistent even while the hub writes.
func snapshot(ctx context.Context, dbPath string) (string, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("no hub here: %w", err)
	}
	db, err := config.NewSQLite(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	snap := filepath.Join(os.TempDir(), fmt.Sprintf("walroos-hub-%d.db", time.Now().UnixNano()))
	_, err = db.Write.ExecContext(ctx, `VACUUM INTO ?`, snap)
	return snap, err
}

func writeArchive(w io.Writer, data, snap, name string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	m, _ := json.MarshalIndent(manifest{Version: version.Version, Hostname: name, CreatedAt: time.Now().UTC()}, "", "  ")
	steps := []func() error{
		func() error { return addBytes(tw, manifestName, m, 0o644) },
		func() error { return addFile(tw, "hub.db", snap) },
		func() error { return addFile(tw, "secret.key", filepath.Join(data, "secret.key")) },
		func() error { return addDir(tw, "tsnet", filepath.Join(data, "tsnet")) },
		tw.Close,
		gz.Close,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func addBytes(tw *tar.Writer, name string, b []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func addFile(tw *tar.Writer, name, src string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return addBytes(tw, name, b, 0o600)
}

// addDir adds the regular files under src as name/...; sockets and the like are skipped.
func addDir(tw *tar.Writer, name, src string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		return addFile(tw, path.Join(name, filepath.ToSlash(rel)), p)
	})
}

// restore unpacks a backup into data. It refuses an existing hub unless force, which moves the
// old data dir aside first.
func restore(file, data string, force bool) (manifest, error) {
	if _, err := os.Stat(filepath.Join(data, "hub.db")); err == nil {
		if !force {
			return manifest{}, fmt.Errorf("%s already has a hub; pass --force to move it aside", data)
		}
		if err := os.Rename(data, data+".before-restore-"+time.Now().Format("20060102-150405")); err != nil {
			return manifest{}, err
		}
	}
	f, err := os.Open(file)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()
	return extract(f, data)
}

// extract writes the archive's files under data. Only the backup's own names are accepted, so a
// crafted archive can't write elsewhere.
func extract(r io.Reader, data string) (manifest, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return manifest{}, fmt.Errorf("not a walroos backup: %w", err)
	}
	var m manifest
	seen := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, err
		}
		if err := extractOne(tr, h, data, &m); err != nil {
			return m, err
		}
		seen[strings.SplitN(h.Name, "/", 2)[0]] = true
	}
	for _, need := range []string{manifestName, "hub.db", "secret.key"} {
		if !seen[need] {
			return m, fmt.Errorf("not a walroos backup: no %s in it", need)
		}
	}
	return m, nil
}

func extractOne(tr *tar.Reader, h *tar.Header, data string, m *manifest) error {
	name := path.Clean(h.Name)
	top := strings.SplitN(name, "/", 2)[0]
	if h.Typeflag != tar.TypeReg || strings.HasPrefix(name, "/") || strings.Contains(name, "..") ||
		(name != manifestName && name != "hub.db" && name != "secret.key" && top != "tsnet") {
		return fmt.Errorf("unexpected entry %q in the backup", h.Name)
	}
	b, err := io.ReadAll(io.LimitReader(tr, 1<<30))
	if err != nil {
		return err
	}
	if name == manifestName {
		return json.Unmarshal(b, m)
	}
	dst := filepath.Join(data, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
