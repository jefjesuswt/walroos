// Package databases runs database services (Postgres, MySQL, MariaDB, Redis, Valkey, MongoDB) as
// Quadlets on a prod node, with their dumps: backups, restore and download.
package databases

import (
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
	"github.com/jefjesuswt/walroos/shared/names"
)

// Databases shares the node's Quadlet host with apps: same unit directory, same locks.
type Databases struct{ q *quadlet.Host }

func New(q *quadlet.Host) *Databases { return &Databases{q: q} }

var (
	ErrInvalid  = errors.New("invalid database: name must be a DNS label, engine and version one of the offered ones, user and database letters, digits and '_', password 16+ letters and digits")
	ErrNotFound = errors.New("database not found on this node")
	ErrNoBackup = errors.New("backup not found")
)

// backups is where dumps go: outside the data root, so no app volume can mount them.
func (a *Databases) backups(name string) string {
	return filepath.Join(filepath.Dir(a.q.Data), "walroos-backups", name)
}

// CreateDatabase writes the database's Quadlet and starts it. Its data lives in the named volume
// walroos-db-<name>, labeled, so deleting it can find it. Like apps it has no host port, joins the
// app network and gets the egress guard: a database never needs the LAN.
func (a *Databases) CreateDatabase(ctx context.Context, spec agent.DatabaseSpec) (agent.Database, error) {
	if !spec.Valid() {
		return agent.Database{}, ErrInvalid
	}
	e, _ := engineOf(spec.Engine)
	unlock, err := a.q.Lock("db/" + spec.Name)
	if err != nil {
		return agent.Database{}, err
	}
	defer unlock()
	c := agent.DatabaseContainer(spec.Name)
	if _, err := os.Stat(filepath.Join(a.q.Dir, c+".container")); err == nil {
		return agent.Database{}, fmt.Errorf("%w: %s already exists on this node", ErrInvalid, c)
	}
	if err := a.writeUnit(ctx, spec, e, c); err != nil {
		return agent.Database{}, err
	}
	if err := host.Run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return agent.Database{}, err
	}
	// --no-block: the first start pulls the image, which can take longer than the hub waits.
	if err := host.Run(ctx, nil, "systemctl", "--user", "start", "--no-block", c+".service"); err != nil {
		return agent.Database{}, err
	}
	return agent.Database{Name: spec.Name, Engine: spec.Engine, Version: spec.Version, Container: c, State: "activating"}, nil
}

// writeUnit makes the network and the data volume, writes the env file (0600) and the unit.
func (a *Databases) writeUnit(ctx context.Context, spec agent.DatabaseSpec, e engine, c string) error {
	if err := quadlet.EnsureNetwork(ctx, spec.Network); err != nil {
		return err
	}
	if err := host.EnsureVolume(ctx, c, quadlet.LabelDB, spec.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(a.q.Dir, 0o755); err != nil {
		return err
	}
	envFile := filepath.Join(a.q.Dir, c+".env")
	if err := os.WriteFile(envFile, []byte(strings.Join(e.env(spec.User, spec.DB, spec.Password), "\n")+"\n"), 0o600); err != nil {
		return err
	}
	guard, err := a.q.Guard(ctx, c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(a.q.Dir, c+".container"), []byte(dbUnit(spec, e, c, envFile, guard)), 0o644)
}

// dbUnit is the database's Quadlet .container file.
func dbUnit(spec agent.DatabaseSpec, e engine, c, envFile, guard string) string {
	image := fmt.Sprintf(e.image, spec.Version)
	execLine := ""
	if e.exec != "" {
		execLine = "Exec=" + e.exec + "\n"
	}
	return fmt.Sprintf(`# Written by walroos. Deleting the database removes it; don't edit.
[Unit]
Description=walroos database %[1]s (%[2]s %[3]s)

[Container]
Image=%[4]s
ContainerName=%[5]s
Network=%[6]s:alias=%[1]s
Label=%[7]s=%[1]s
Label=walroos.engine=%[2]s
Label=walroos.version=%[3]s
EnvironmentFile=%[8]s
Volume=%[5]s:%[9]s
NoNewPrivileges=true
# ponytail: same limits for every database; per-database limits when one needs more
Memory=2g
PidsLimit=2048
%[10]s
[Quadlet]
DefaultDependencies=false

[Service]
Restart=always
# A first start pulls the image.
TimeoutStartSec=900
%[11]s

[Install]
WantedBy=default.target
`, spec.Name, spec.Engine, spec.Version, image, c, cmp.Or(spec.Network, agent.AppNetwork), quadlet.LabelDB, envFile, e.data(spec.Version), execLine, guard)
}

// ListDatabases reads the Quadlets labeled as databases and asks systemd for their state.
func (a *Databases) ListDatabases(ctx context.Context) ([]agent.Database, error) {
	files, err := filepath.Glob(filepath.Join(a.q.Dir, "walroos-db-*.container"))
	if err != nil {
		return nil, err
	}
	list := []agent.Database{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := quadlet.UnitValue(b, "Label="+quadlet.LabelDB+"=")
		if name == "" {
			continue
		}
		c := strings.TrimSuffix(filepath.Base(f), ".container")
		state, _ := exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", "ActiveState", "--value", c+".service").Output()
		list = append(list, agent.Database{Name: name, Engine: quadlet.UnitValue(b, "Label=walroos.engine="), Version: quadlet.UnitValue(b, "Label=walroos.version="),
			Container: c, State: strings.TrimSpace(string(state))})
	}
	return list, nil
}

func (a *Databases) database(ctx context.Context, name string) (agent.Database, engine, error) {
	list, err := a.ListDatabases(ctx)
	if err != nil {
		return agent.Database{}, engine{}, err
	}
	for _, d := range list {
		if d.Name == name {
			e, ok := engineOf(d.Engine)
			if !ok {
				return agent.Database{}, engine{}, fmt.Errorf("database %s has an unknown engine %q", name, d.Engine)
			}
			return d, e, nil
		}
	}
	return agent.Database{}, engine{}, ErrNotFound
}

// DeleteDatabase stops it and removes its unit. purge also removes its data volume and backups;
// without it a database created again with the same name finds its data.
func (a *Databases) DeleteDatabase(ctx context.Context, name string, purge bool) error {
	if !names.DNSLabel(name) {
		return ErrNotFound
	}
	unlock, err := a.q.Lock("db/" + name)
	if err != nil {
		return err
	}
	defer unlock()
	a.q.Remove(ctx, agent.DatabaseContainer(name))
	if !purge {
		return nil
	}
	vols, err := host.Podman(ctx, "volume", "ls", "-q", "--filter", "label="+quadlet.LabelDB+"="+name)
	if err != nil {
		return err
	}
	for _, v := range strings.Fields(vols) {
		if _, err := host.Podman(ctx, "volume", "rm", v); err != nil {
			return err
		}
	}
	return os.RemoveAll(a.backups(name))
}

// waitReady runs the engine's readiness check until it passes: a database that was just
// created, or restarted by a restore, takes a few seconds to accept connections.
func waitReady(ctx context.Context, d agent.Database, e engine, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if _, err := host.Podman(ctx, "exec", d.Container, "sh", "-c", e.ready); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s isn't accepting connections (state %s)", d.Name, d.State)
		case <-time.After(time.Second):
		}
	}
}

// BackupDatabase dumps the database to a gzip file in its backups dir and keeps the newest keep.
func (a *Databases) BackupDatabase(ctx context.Context, name string, keep int) (agent.Backup, error) {
	d, e, err := a.database(ctx, name)
	if err != nil {
		return agent.Backup{}, err
	}
	unlock, err := a.q.Lock("db/" + name)
	if err != nil {
		return agent.Backup{}, err
	}
	defer unlock()
	if err := waitReady(ctx, d, e, 30*time.Second); err != nil {
		return agent.Backup{}, err
	}
	dir := a.backups(name)
	now := time.Now().UTC()
	file := fmt.Sprintf("%s-%s.%s.gz", name, now.Format("20060102T150405Z"), e.ext)
	if err := dump(ctx, d.Container, e.dump, dir, file); err != nil {
		return agent.Backup{}, err
	}
	list, _ := a.listBackups(name)
	for i, b := range list { // newest first
		if i >= max(keep, 1) {
			os.Remove(filepath.Join(dir, b.File))
		}
	}
	st, err := os.Stat(filepath.Join(dir, file))
	if err != nil {
		return agent.Backup{}, err
	}
	return agent.Backup{File: file, Size: st.Size(), CreatedAt: now}, nil
}

// dump runs the engine's dump script in the container, gzipped into dir/file. It writes a
// partial file first and renames it, so a failed dump never looks like a backup.
func dump(ctx context.Context, container, script, dir, file string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".partial-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	zw := gzip.NewWriter(tmp)
	cmd := exec.CommandContext(ctx, "podman", "exec", container, "sh", "-c", script)
	cmd.Stdout = zw
	err = host.RunCmd(cmd)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, file))
}

// ListBackups lists a database's dumps, newest first.
func (a *Databases) ListBackups(ctx context.Context, name string) ([]agent.Backup, error) {
	if _, _, err := a.database(ctx, name); err != nil {
		return nil, err
	}
	return a.listBackups(name)
}

func (a *Databases) listBackups(name string) ([]agent.Backup, error) {
	entries, err := os.ReadDir(a.backups(name))
	if errors.Is(err, os.ErrNotExist) {
		return []agent.Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []agent.Backup{}
	for _, en := range entries {
		info, err := en.Info()
		if err != nil || !agent.ValidBackupFile(en.Name()) || !info.Mode().IsRegular() {
			continue
		}
		ts := strings.TrimPrefix(en.Name(), name+"-")
		created, err := time.Parse("20060102T150405Z", strings.SplitN(ts, ".", 2)[0])
		if err != nil {
			continue // another database's file whose name starts with ours
		}
		list = append(list, agent.Backup{File: en.Name(), Size: info.Size(), CreatedAt: created})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}

// OpenBackup opens a dump for download. The name is checked against the dir's own files.
func (a *Databases) OpenBackup(ctx context.Context, name, file string) (*os.File, error) {
	list, err := a.ListBackups(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if b.File == file {
			return os.Open(filepath.Join(a.backups(name), file))
		}
	}
	return nil, ErrNoBackup
}

// RestoreDatabase feeds a dump back into the database, replacing what's there.
func (a *Databases) RestoreDatabase(ctx context.Context, name, file string) error {
	f, err := a.OpenBackup(ctx, name, file)
	if err != nil {
		return err
	}
	defer f.Close()
	d, e, err := a.database(ctx, name)
	if err != nil {
		return err
	}
	unlock, err := a.q.Lock("db/" + name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := waitReady(ctx, d, e, 30*time.Second); err != nil {
		return err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	cmd := exec.CommandContext(ctx, "podman", "exec", "-i", d.Container, "sh", "-c", e.restore)
	cmd.Stdin = io.Reader(zr)
	if err := host.RunCmd(cmd); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if e.reload != "" {
		host.Podman(ctx, "exec", d.Container, "sh", "-c", e.reload) // the container exits under it: exit 137
		time.Sleep(2 * time.Second)                                 // let systemd see it go before checking it's back
	}
	return waitReady(ctx, d, e, 60*time.Second)
}
