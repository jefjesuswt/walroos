package databases

import "github.com/jefjesuswt/walroos/internal/agent"

// runtime is how an engine runs on a node. Its scripts run inside the container with `sh -c`,
// with the env file's variables set: ready exits 0 once it accepts connections, dump writes a
// backup to stdout, restore reads one from stdin.
type runtime struct {
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

// engine is an engine and how it runs here.
type engine struct {
	agent.Engine
	runtime
}

var runtimes = map[string]runtime{
	"postgres": {
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
	"mysql": {
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
	"mariadb": {
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
	"redis": {
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
	"valkey": {
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
	"mongo": {
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

func engineOf(id string) (engine, bool) {
	e, ok := agent.EngineByID(id)
	if !ok {
		return engine{}, false
	}
	return engine{e, runtimes[id]}, true
}
