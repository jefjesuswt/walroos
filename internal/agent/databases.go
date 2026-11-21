package agent

import (
	"regexp"
	"slices"
	"time"

	"github.com/jefjesuswt/walroos/shared/names"
)

// Engine is a database walroos can run.
type Engine struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Versions []string `json:"versions"` // image tags, newest first
	Port     int      `json:"port"`
	Scheme   string   `json:"scheme"` // of the connection URL apps get
}

// Engines are pulled from Docker Hub's official images. How each runs on a node is in
// internal/agent/databases.
var Engines = []Engine{
	{ID: "postgres", Label: "PostgreSQL", Versions: []string{"18", "17", "16"}, Port: 5432, Scheme: "postgres"},
	{ID: "mysql", Label: "MySQL", Versions: []string{"8.4"}, Port: 3306, Scheme: "mysql"},
	{ID: "mariadb", Label: "MariaDB", Versions: []string{"11.8", "11.4"}, Port: 3306, Scheme: "mysql"},
	{ID: "redis", Label: "Redis", Versions: []string{"8", "7"}, Port: 6379, Scheme: "redis"},
	{ID: "valkey", Label: "Valkey", Versions: []string{"8"}, Port: 6379, Scheme: "redis"},
	{ID: "mongo", Label: "MongoDB", Versions: []string{"8", "7"}, Port: 27017, Scheme: "mongodb"},
}

// EngineByID finds an engine in Engines.
func EngineByID(id string) (Engine, bool) {
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
	passwordRe = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`) // no quoting needed in an env file or a URL
	backupRe   = regexp.MustCompile(`^[a-z0-9-]+-\d{8}T\d{6}Z\.(sql|rdb|archive)\.gz$`)
)

// ValidPassword: what a database password may be (the hub checks a chosen one with it too).
func ValidPassword(s string) bool { return passwordRe.MatchString(s) }

// ValidBackupFile: the name of a dump file this agent writes, safe in a path and a header.
func ValidBackupFile(s string) bool { return backupRe.MatchString(s) }

func (spec DatabaseSpec) Valid() bool {
	e, ok := EngineByID(spec.Engine)
	return ok && slices.Contains(e.Versions, spec.Version) && names.DNSLabel(spec.Name) &&
		names.SQLIdent(spec.User) && names.SQLIdent(spec.DB) && passwordRe.MatchString(spec.Password) && ValidNetwork(spec.Network)
}

// DatabaseContainer is the container (and host name) of a database: apps reach it on its project's network.
func DatabaseContainer(name string) string { return "walroos-db-" + name }
