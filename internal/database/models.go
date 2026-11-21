// Package database is the hub's view of database services: Postgres, MySQL, MariaDB or Redis
// running as a Quadlet on a prod node, their credentials (sealed), and scheduled backups. The
// node keeps the container, the data volume and the dump files; the hub keeps the config.
package database

import (
	"time"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNotFound    = whttp.NotFound("database not found")
	ErrExists      = whttp.Conflict("a database with that name already exists")
	ErrInvalid     = whttp.Invalid("name must be a DNS label; engine and version one of GET /api/databases/engines; database and user lowercase letters, digits and '_' (not starting with a digit); password 16 to 128 letters and digits; description at most 500 characters")
	ErrSchedule    = whttp.Invalid("backup_every_hours must be 0 (off) to 720, backup_keep 1 to 100")
	ErrNoNode      = whttp.Conflict("no online node with the prod role; join one with `walroos node join --roles prod`")
	ErrNodeRole    = whttp.Conflict("the chosen node doesn't have the prod role")
	ErrOtherNode   = whttp.Invalid("the app runs on another node: apps only reach databases on their own node")
	ErrOtherProj   = whttp.Invalid("the app is in another project: apps only reach databases of their own project")
	ErrEnvKey      = whttp.Invalid("key must be an env variable name (A-Z, 0-9, _)")
	ErrBusy        = whttp.Conflict("a backup, restore or delete of this database is running; try again when it ends")
	ErrNoBackup    = whttp.NotFound("backup not found on the node")
	ErrInvalidFile = whttp.Invalid("not a backup file name")
)

// Database is a database service and how apps reach it.
type Database struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	ProjectID   string `json:"project_id"`
	Project     string `json:"project"` // its name; the database is on the project's network
	Engine      string `json:"engine"`
	Version     string `json:"version"`
	User        string `json:"user"`
	Description string `json:"description"`
	DB          string `json:"db"`
	Host        string `json:"host"` // container name on the node's walroos network
	Port        int    `json:"port"`
	// URL is the connection string apps use (with the password). Only on Get: lists don't carry it.
	URL string `json:"url,omitempty"`

	BackupEveryHours int        `json:"backup_every_hours"`
	BackupKeep       int        `json:"backup_keep"`
	LastBackupAt     *time.Time `json:"last_backup_at,omitempty"`
	LastBackupError  string     `json:"last_backup_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`

	State    string `json:"state,omitempty"` // systemd ActiveState on the node; "" if it didn't answer
	NodeDown bool   `json:"node_down,omitempty"`

	password string // sealed, from the store
}

// CreateInput is what the UI or API sends. Node is a name or ID; "" picks an online prod node.
type CreateInput struct {
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Node    string `json:"node"`
	Project string `json:"project"` // name; "" = default
	// Optional, like Dokploy's form: "" derives db and user from the name and generates a password.
	Description string `json:"description"`
	DB          string `json:"db"`
	User        string `json:"user"`
	Password    string `json:"password"`
}

// Schedule is when backups run and how many the node keeps.
type Schedule struct {
	EveryHours int `json:"backup_every_hours"`
	Keep       int `json:"backup_keep"`
}

// LinkInput puts the database's URL in an app's environment under Key.
type LinkInput struct {
	App string `json:"app"`
	Key string `json:"key"` // "" = DATABASE_URL (REDIS_URL for redis)
}
