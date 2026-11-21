//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDatabasePodman runs each engine as a Quadlet on real podman and systemd --user: write a
// row, back up, change it, restore, and the row is back. Pulls the official images.
//
//	WALROOS_PODMAN_TEST=1 go test ./internal/agent -run DatabasePodman -v
func TestDatabasePodman(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1")
	}
	// What to write, change and read back, per engine. Each script runs in the container.
	cases := []struct {
		engine, version    string
		set, change, check string
		want               string
	}{
		{"postgres", "17",
			`psql -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "CREATE TABLE t (v text); INSERT INTO t VALUES ('before');"`,
			`psql -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "UPDATE t SET v = 'after'; CREATE TABLE extra (x int);"`,
			`psql -qtA -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT v FROM t"`, "before"},
		{"redis", "8",
			`redis-cli SET k before >/dev/null`, `redis-cli SET k after >/dev/null`, `redis-cli GET k`, "before"},
		{"mariadb", "11.8",
			`MYSQL_PWD="$MARIADB_PASSWORD" mariadb -h 127.0.0.1 -u "$MARIADB_USER" "$MARIADB_DATABASE" -e "CREATE TABLE t (v text); INSERT INTO t VALUES ('before');"`,
			`MYSQL_PWD="$MARIADB_PASSWORD" mariadb -h 127.0.0.1 -u "$MARIADB_USER" "$MARIADB_DATABASE" -e "UPDATE t SET v = 'after';"`,
			`MYSQL_PWD="$MARIADB_PASSWORD" mariadb -h 127.0.0.1 -u "$MARIADB_USER" "$MARIADB_DATABASE" -N -e "SELECT v FROM t"`, "before"},
		{"mysql", "8.4",
			`MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u "$MYSQL_USER" "$MYSQL_DATABASE" -e "CREATE TABLE t (v text); INSERT INTO t VALUES ('before');"`,
			`MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u "$MYSQL_USER" "$MYSQL_DATABASE" -e "UPDATE t SET v = 'after';"`,
			`MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u "$MYSQL_USER" "$MYSQL_DATABASE" -N -e "SELECT v FROM t"`, "before"},
		{"postgres", "18",
			`psql -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "CREATE TABLE t (v text); INSERT INTO t VALUES ('before');"`,
			`psql -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "UPDATE t SET v = 'after';"`,
			`psql -qtA -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT v FROM t"`, "before"},
	}
	for _, c := range cases {
		t.Run(c.engine+"-"+c.version, func(t *testing.T) {
			ctx := context.Background()
			a := NewApps()
			a.data = filepath.Join(t.TempDir(), "walroos-data") // backups land next to it, not in $HOME
			name := fmt.Sprintf("t%s%s%d", c.engine[:2], strings.ReplaceAll(c.version, ".", ""), time.Now().UnixNano()%1e6)
			t.Cleanup(func() { a.DeleteDatabase(context.Background(), name, true) })

			spec := DatabaseSpec{Name: name, Engine: c.engine, Version: c.version, User: "app", DB: "app", Password: "s3cretPassw0rdXYZ"}
			if _, err := a.CreateDatabase(ctx, spec); err != nil {
				t.Fatal(err)
			}
			if _, err := a.CreateDatabase(ctx, spec); !errors.Is(err, ErrInvalidDatabase) {
				t.Errorf("second create: %v, want ErrInvalidDatabase", err)
			}
			d, e, err := a.database(ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			if err := waitReady(ctx, d, e, 5*time.Minute); err != nil { // first run pulls the image
				t.Fatal(err)
			}
			sh := func(script string) string {
				t.Helper()
				out, err := podman(ctx, "exec", d.Container, "sh", "-c", script)
				if err != nil {
					t.Fatalf("%s: %v", script, err)
				}
				return strings.TrimSpace(out)
			}
			// The password is required: without it the client is refused.
			if c.engine == "redis" {
				if out, _ := podman(ctx, "exec", "-e", "REDISCLI_AUTH=", d.Container, "redis-cli", "GET", "k"); !strings.Contains(out, "NOAUTH") {
					t.Errorf("redis without a password answered %q", out)
				}
			}
			// The password never reaches the unit file.
			unit, _ := os.ReadFile(filepath.Join(a.dir, d.Container+".container"))
			if strings.Contains(string(unit), spec.Password) || !strings.Contains(string(unit), "Network="+AppNetwork) {
				t.Errorf("unit:\n%s", unit)
			}

			sh(c.set)
			b, err := a.BackupDatabase(ctx, name, 2)
			if err != nil {
				t.Fatal(err)
			}
			if b.Size == 0 || !backupRe.MatchString(b.File) {
				t.Errorf("backup = %+v", b)
			}
			sh(c.change)
			if got := sh(c.check); got != "after" {
				t.Fatalf("after change: %q", got)
			}
			if err := a.RestoreDatabase(ctx, name, b.File); err != nil {
				t.Fatal(err)
			}
			d, _, _ = a.database(ctx, name)
			if got := sh(c.check); got != c.want {
				t.Errorf("after restore: %q, want %q", got, c.want)
			}

			// keep=2: a third backup drops the oldest. Names have second precision.
			time.Sleep(1100 * time.Millisecond)
			a.BackupDatabase(ctx, name, 2)
			time.Sleep(1100 * time.Millisecond)
			last, err := a.BackupDatabase(ctx, name, 2)
			if err != nil {
				t.Fatal(err)
			}
			list, _ := a.ListBackups(ctx, name)
			if len(list) != 2 || list[0].File != last.File {
				t.Errorf("backups = %+v, want the 2 newest", list)
			}
			for _, bad := range []string{"../../etc/passwd", b.File, "x.sql.gz"} {
				if err := a.RestoreDatabase(ctx, name, bad); !errors.Is(err, ErrBackupNotFound) {
					t.Errorf("restore %q: %v, want ErrBackupNotFound", bad, err)
				}
			}

			// Delete without purge keeps the data volume; purge removes it and the backups.
			if err := a.DeleteDatabase(ctx, name, false); err != nil {
				t.Fatal(err)
			}
			if _, err := podman(ctx, "volume", "exists", d.Container); err != nil {
				t.Error("delete without purge removed the data volume")
			}
			if err := a.DeleteDatabase(ctx, name, true); err != nil {
				t.Fatal(err)
			}
			if _, err := podman(ctx, "volume", "exists", d.Container); err == nil {
				t.Error("purge left the data volume")
			}
			if _, err := os.Stat(a.backups(name)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("purge left the backups: %v", err)
			}
		})
	}
}
