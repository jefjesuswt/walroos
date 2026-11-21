package agent

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidDatabase  = errors.New("invalid database: name must be a DNS label, engine and version one of the offered ones, user and database letters, digits and '_', password 16+ letters and digits")
	ErrDatabaseNotFound = errors.New("database not found on this node")
	ErrBackupNotFound   = errors.New("backup not found")
)

const labelDB = "walroos.db"

// Engine is a database walroos can run. Its scripts run inside the container with `sh -c`, with
// the env file's variables set: Ready exits 0 once it accepts connections, Dump writes a backup
// to stdout, Restore reads one from stdin.
type Engine struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Versions []string `json:"versions"` // image tags, newest first
	Port     int      `json:"port"`
	Scheme   string   `json:"scheme"` // of the connection URL apps get

	image   string // docker.io/library/<x>:%s
	data    func(version string) string
	env     func(user, db, password string) []string
	exec    string // Exec= of the unit, "" = the image's own command
	ready   string
	dump    string
	restore string
	reload  string // after restore, errors ignored: it may stop the container (redis loads its file at start)
	ext     string // backup file extension, before .gz
}

// Engines are pulled from Docker Hub's official images.
var Engines = []Engine{
	{
		ID: "postgres", Label: "PostgreSQL", Versions: []string{"18", "17", "16"}, Port: 5432, Scheme: "postgres",
		image: "docker.io/library/postgres:%s",
		// 18 keeps its data in a versioned dir under /var/lib/postgresql (pg_upgrade friendly).
		data: func(v string) string {
			if v == "18" {
				return "/var/lib/postgresql"
			}
			return "/var/lib/postgresql/data"
		},
		env: func(user, db, pw string) []string {
			return []string{"POSTGRES_USER=" + user, "POSTGRES_DB=" + db, "POSTGRES_PASSWORD=" + pw}
		},
		// Over TCP: during init the entrypoint's temporary server only listens on the socket.
		ready: `pg_isready -q -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"`,
		// --clean --if-exists: the dump drops what it recreates, so a restore replaces the data.
		dump:    `pg_dump --clean --if-exists --no-owner -U "$POSTGRES_USER" "$POSTGRES_DB"`,
		restore: `psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null`,
		ext:     "sql",
	},
	{
		ID: "mysql", Label: "MySQL", Versions: []string{"8.4"}, Port: 3306, Scheme: "mysql",
		image: "docker.io/library/mysql:%s",
		data:  func(string) string { return "/var/lib/mysql" },
		env: func(user, db, pw string) []string {
			return []string{"MYSQL_USER=" + user, "MYSQL_DATABASE=" + db, "MYSQL_PASSWORD=" + pw, "MYSQL_RANDOM_ROOT_PASSWORD=yes"}
		},
		// MYSQL_PWD keeps the password out of argv inside the container too.
		ready:   `MYSQL_PWD="$MYSQL_PASSWORD" mysqladmin ping -h 127.0.0.1 -u "$MYSQL_USER" --silent`,
		dump:    `MYSQL_PWD="$MYSQL_PASSWORD" mysqldump -h 127.0.0.1 -u "$MYSQL_USER" --single-transaction --no-tablespaces "$MYSQL_DATABASE"`,
		restore: `MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u "$MYSQL_USER" "$MYSQL_DATABASE"`,
		ext:     "sql",
	},
	{
		ID: "mariadb", Label: "MariaDB", Versions: []string{"11.8", "11.4"}, Port: 3306, Scheme: "mysql",
		image: "docker.io/library/mariadb:%s",
		data:  func(string) string { return "/var/lib/mysql" },
		env: func(user, db, pw string) []string {
			return []string{"MARIADB_USER=" + user, "MARIADB_DATABASE=" + db, "MARIADB_PASSWORD=" + pw, "MARIADB_RANDOM_ROOT_PASSWORD=yes"}
		},
		ready:   `MYSQL_PWD="$MARIADB_PASSWORD" mariadb-admin ping -h 127.0.0.1 -u "$MARIADB_USER" --silent`,
		dump:    `MYSQL_PWD="$MARIADB_PASSWORD" mariadb-dump -h 127.0.0.1 -u "$MARIADB_USER" --single-transaction --no-tablespaces "$MARIADB_DATABASE"`,
		restore: `MYSQL_PWD="$MARIADB_PASSWORD" mariadb -h 127.0.0.1 -u "$MARIADB_USER" "$MARIADB_DATABASE"`,
		ext:     "sql",
	},
	{
		ID: "redis", Label: "Redis", Versions: []string{"8", "7"}, Port: 6379, Scheme: "redis",
		image: "docker.io/library/redis:%s",
		data:  func(string) string { return "/data" },
		env:   func(_, _, pw string) []string { return []string{"REDIS_PASSWORD=" + pw, "REDISCLI_AUTH=" + pw} },
		// RDB snapshots only (no AOF), so a backup is one file and a restore is putting it back.
		// "$$" is a literal "$" for systemd.
		exec:    `/bin/sh -c 'exec redis-server --save 300 1 --appendonly no --requirepass "$$REDIS_PASSWORD"'`,
		ready:   `redis-cli ping | grep -q PONG`,
		dump:    `redis-cli SAVE >/dev/null && cat /data/dump.rdb`,
		restore: `cat > /data/dump.rdb.new && mv /data/dump.rdb.new /data/dump.rdb`,
		// SHUTDOWN NOSAVE leaves the file alone; systemd starts redis again and it loads it.
		reload: `redis-cli SHUTDOWN NOSAVE`,
		ext:    "rdb",
	},
	{
		ID: "valkey", Label: "Valkey", Versions: []string{"8"}, Port: 6379, Scheme: "redis",
		image: "docker.io/valkey/valkey:%s",
		data:  func(string) string { return "/data" },
		env: func(_, _, pw string) []string {
			return []string{"REDIS_PASSWORD=" + pw, "REDISCLI_AUTH=" + pw, "VALKEYCLI_AUTH=" + pw}
		},
		// Redis's open-source fork, same files and commands.
		exec:    `/bin/sh -c 'exec valkey-server --save 300 1 --appendonly no --requirepass "$$REDIS_PASSWORD"'`,
		ready:   `valkey-cli ping | grep -q PONG`,
		dump:    `valkey-cli SAVE >/dev/null && cat /data/dump.rdb`,
		restore: `cat > /data/dump.rdb.new && mv /data/dump.rdb.new /data/dump.rdb`,
		// SHUTDOWN NOSAVE leaves the file alone; systemd starts redis again and it loads it.
		reload: `valkey-cli SHUTDOWN NOSAVE`,
		ext:    "rdb",
	},
	{
		ID: "mongo", Label: "MongoDB", Versions: []string{"8", "7"}, Port: 27017, Scheme: "mongodb",
		image: "docker.io/library/mongo:%s",
		data:  func(string) string { return "/data/db" },
		// The user is root on the admin database, like the image's entrypoint makes it.
		env: func(user, db, pw string) []string {
			return []string{"MONGO_INITDB_ROOT_USERNAME=" + user, "MONGO_INITDB_ROOT_PASSWORD=" + pw, "MONGO_INITDB_DATABASE=" + db}
		},
		// ponytail: mongosh and the tools take the password as an argument (inside the container).
		ready:   `mongosh --quiet --host 127.0.0.1 -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin --eval 'db.runCommand({ping: 1}).ok' | grep -q 1`,
		dump:    `mongodump --quiet --archive -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin --db "$MONGO_INITDB_DATABASE"`,
		restore: `mongorestore --quiet --archive --drop -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin`,
		ext:     "archive",
	},
}

func engine(id string) (Engine, bool) {
	for _, e := range Engines {
		if e.ID == id {
			return e, true
		}
	}
	return Engine{}, false
}

// DatabaseSpec is what the hub sends to create a database. The password goes to an env file.
type DatabaseSpec struct {
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Version  string `json:"version"`
	User     string `json:"user"`
	DB       string `json:"db"`
	Password string `json:"password"`
	Network  string `json:"network,omitempty"` // the project's network (ProjectNetwork); "" = AppNetwork
}

// Database is a database's state on this node.
type Database struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	Container string `json:"container"` // also its host name on the walroos network
	State     string `json:"state"`     // systemd ActiveState
}

// Backup is one dump file on the node.
type Backup struct {
	File      string    `json:"file"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	identRe    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	passwordRe = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`) // no quoting needed in an env file or a URL
	backupRe   = regexp.MustCompile(`^[a-z0-9-]+-\d{8}T\d{6}Z\.(sql|rdb|archive)\.gz$`)
)

func (spec DatabaseSpec) Valid() bool {
	e, ok := engine(spec.Engine)
	return ok && slices.Contains(e.Versions, spec.Version) && workspaceNameRe.MatchString(spec.Name) &&
		identRe.MatchString(spec.User) && identRe.MatchString(spec.DB) && passwordRe.MatchString(spec.Password) && validNetwork(spec.Network)
}

// DatabaseContainer is the container (and host name) of a database: apps reach it on its project's network.
func DatabaseContainer(name string) string { return "walroos-db-" + name }

// backups is where dumps go: outside the data root, so no app volume can mount them.
func (a *Apps) backups(name string) string {
	return filepath.Join(filepath.Dir(a.data), "walroos-backups", name)
}

// CreateDatabase writes the database's Quadlet and starts it. Its data lives in the named volume
// walroos-db-<name>, labeled, so deleting it can find it. Like apps it has no host port, joins the
// app network and gets the egress guard: a database never needs the LAN.
func (a *Apps) CreateDatabase(ctx context.Context, spec DatabaseSpec) (Database, error) {
	if !spec.Valid() {
		return Database{}, ErrInvalidDatabase
	}
	e, _ := engine(spec.Engine)
	unlock, err := a.lock("db/" + spec.Name)
	if err != nil {
		return Database{}, err
	}
	defer unlock()
	c := DatabaseContainer(spec.Name)
	if _, err := os.Stat(filepath.Join(a.dir, c+".container")); err == nil {
		return Database{}, fmt.Errorf("%w: %s already exists on this node", ErrInvalidDatabase, c)
	}
	if err := ensureServiceNetwork(ctx, spec.Network); err != nil {
		return Database{}, err
	}
	if err := ensureVolume(ctx, c, labelDB, spec.Name); err != nil {
		return Database{}, err
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return Database{}, err
	}
	envFile := filepath.Join(a.dir, c+".env")
	if err := os.WriteFile(envFile, []byte(strings.Join(e.env(spec.User, spec.DB, spec.Password), "\n")+"\n"), 0o600); err != nil {
		return Database{}, err
	}
	guard, err := a.guard(ctx, c)
	if err != nil {
		return Database{}, err
	}
	image := fmt.Sprintf(e.image, spec.Version)
	execLine := ""
	if e.exec != "" {
		execLine = "Exec=" + e.exec + "\n"
	}
	unit := fmt.Sprintf(`# Written by walroos. Deleting the database removes it; don't edit.
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
`, spec.Name, spec.Engine, spec.Version, image, c, cmpOr(spec.Network, AppNetwork), labelDB, envFile, e.data(spec.Version), execLine, guard)
	if err := os.WriteFile(filepath.Join(a.dir, c+".container"), []byte(unit), 0o644); err != nil {
		return Database{}, err
	}
	if err := run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return Database{}, err
	}
	// --no-block: the first start pulls the image, which can take longer than the hub waits.
	if err := run(ctx, nil, "systemctl", "--user", "start", "--no-block", c+".service"); err != nil {
		return Database{}, err
	}
	return Database{Name: spec.Name, Engine: spec.Engine, Version: spec.Version, Container: c, State: "activating"}, nil
}

// ListDatabases reads the Quadlets labeled as databases and asks systemd for their state.
func (a *Apps) ListDatabases(ctx context.Context) ([]Database, error) {
	files, err := filepath.Glob(filepath.Join(a.dir, "walroos-db-*.container"))
	if err != nil {
		return nil, err
	}
	list := []Database{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := unitValue(b, "Label="+labelDB+"=")
		if name == "" {
			continue
		}
		c := strings.TrimSuffix(filepath.Base(f), ".container")
		state, _ := exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", "ActiveState", "--value", c+".service").Output()
		list = append(list, Database{Name: name, Engine: unitValue(b, "Label=walroos.engine="), Version: unitValue(b, "Label=walroos.version="),
			Container: c, State: strings.TrimSpace(string(state))})
	}
	return list, nil
}

func (a *Apps) database(ctx context.Context, name string) (Database, Engine, error) {
	list, err := a.ListDatabases(ctx)
	if err != nil {
		return Database{}, Engine{}, err
	}
	for _, d := range list {
		if d.Name == name {
			e, ok := engine(d.Engine)
			if !ok {
				return Database{}, Engine{}, fmt.Errorf("database %s has an unknown engine %q", name, d.Engine)
			}
			return d, e, nil
		}
	}
	return Database{}, Engine{}, ErrDatabaseNotFound
}

// DeleteDatabase stops it and removes its unit. purge also removes its data volume and backups;
// without it a database created again with the same name finds its data.
func (a *Apps) DeleteDatabase(ctx context.Context, name string, purge bool) error {
	if !workspaceNameRe.MatchString(name) {
		return ErrDatabaseNotFound
	}
	unlock, err := a.lock("db/" + name)
	if err != nil {
		return err
	}
	defer unlock()
	a.remove(ctx, DatabaseContainer(name))
	if !purge {
		return nil
	}
	vols, err := podman(ctx, "volume", "ls", "-q", "--filter", "label="+labelDB+"="+name)
	if err != nil {
		return err
	}
	for _, v := range strings.Fields(vols) {
		if _, err := podman(ctx, "volume", "rm", v); err != nil {
			return err
		}
	}
	return os.RemoveAll(a.backups(name))
}

// waitReady runs the engine's readiness check until it passes: a database that was just
// created, or restarted by a restore, takes a few seconds to accept connections.
func waitReady(ctx context.Context, d Database, e Engine, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if _, err := podman(ctx, "exec", d.Container, "sh", "-c", e.ready); err == nil {
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
func (a *Apps) BackupDatabase(ctx context.Context, name string, keep int) (Backup, error) {
	d, e, err := a.database(ctx, name)
	if err != nil {
		return Backup{}, err
	}
	unlock, err := a.lock("db/" + name)
	if err != nil {
		return Backup{}, err
	}
	defer unlock()
	if err := waitReady(ctx, d, e, 30*time.Second); err != nil {
		return Backup{}, err
	}
	dir := a.backups(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Backup{}, err
	}
	now := time.Now().UTC()
	file := fmt.Sprintf("%s-%s.%s.gz", name, now.Format("20060102T150405Z"), e.ext)
	tmp, err := os.CreateTemp(dir, ".partial-")
	if err != nil {
		return Backup{}, err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	zw := gzip.NewWriter(tmp)
	cmd := exec.CommandContext(ctx, "podman", "exec", d.Container, "sh", "-c", e.dump)
	cmd.Stdout = zw
	err = runCmd(cmd)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Backup{}, fmt.Errorf("dump: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, file)); err != nil {
		return Backup{}, err
	}
	list, _ := a.listBackups(name)
	for i, b := range list { // newest first
		if i >= max(keep, 1) {
			os.Remove(filepath.Join(dir, b.File))
		}
	}
	st, err := os.Stat(filepath.Join(dir, file))
	if err != nil {
		return Backup{}, err
	}
	return Backup{File: file, Size: st.Size(), CreatedAt: now}, nil
}

// ListBackups lists a database's dumps, newest first.
func (a *Apps) ListBackups(ctx context.Context, name string) ([]Backup, error) {
	if _, _, err := a.database(ctx, name); err != nil {
		return nil, err
	}
	return a.listBackups(name)
}

func (a *Apps) listBackups(name string) ([]Backup, error) {
	entries, err := os.ReadDir(a.backups(name))
	if errors.Is(err, os.ErrNotExist) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []Backup{}
	for _, en := range entries {
		info, err := en.Info()
		if err != nil || !backupRe.MatchString(en.Name()) || !info.Mode().IsRegular() {
			continue
		}
		ts := strings.TrimPrefix(en.Name(), name+"-")
		created, err := time.Parse("20060102T150405Z", strings.SplitN(ts, ".", 2)[0])
		if err != nil {
			continue // another database's file whose name starts with ours
		}
		list = append(list, Backup{File: en.Name(), Size: info.Size(), CreatedAt: created})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}

// OpenBackup opens a dump for download. The name is checked against the dir's own files.
func (a *Apps) OpenBackup(ctx context.Context, name, file string) (*os.File, error) {
	list, err := a.ListBackups(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if b.File == file {
			return os.Open(filepath.Join(a.backups(name), file))
		}
	}
	return nil, ErrBackupNotFound
}

// RestoreDatabase feeds a dump back into the database, replacing what's there.
func (a *Apps) RestoreDatabase(ctx context.Context, name, file string) error {
	f, err := a.OpenBackup(ctx, name, file)
	if err != nil {
		return err
	}
	defer f.Close()
	d, e, err := a.database(ctx, name)
	if err != nil {
		return err
	}
	unlock, err := a.lock("db/" + name)
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
	if err := runCmd(cmd); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if e.reload != "" {
		podman(ctx, "exec", d.Container, "sh", "-c", e.reload) // the container exits under it: exit 137
		time.Sleep(2 * time.Second)                            // let systemd see it go before checking it's back
	}
	return waitReady(ctx, d, e, 60*time.Second)
}
