package fleet

import (
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNodeNotFound          = whttp.NotFound("node not found")
	ErrNodeNameAlreadyExists = whttp.Conflict("node name already exists")
	ErrInvalidJoinToken      = whttp.Unauthorized("join token is invalid, expired or already used")
	ErrUnauthorized          = whttp.Unauthorized("invalid node token")
	ErrInvalidName           = whttp.Invalid("node name must be a DNS label: lowercase letters, digits and '-', max 63 chars")
	ErrInvalidRoles          = whttp.Invalid("roles must be one or more of: dev, build, prod")
)

// Roles a node can take. dev runs workspaces, build runs image builds, prod runs apps.
var Roles = []string{"dev", "build", "prod"}

const (
	// HeartbeatInterval is how often a node agent reports in.
	HeartbeatInterval = 15 * time.Second
	// offlineAfter: three missed heartbeats.
	offlineAfter = 3 * HeartbeatInterval
	joinTokenTTL = time.Hour
)

// Telemetry is what a node agent sends on every heartbeat.
type Telemetry struct {
	Hostname    string  `json:"hostname"`
	OS          string  `json:"os"`
	Arch        string  `json:"arch"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemUsedMB   uint64  `json:"mem_used_mb"`
	MemTotalMB  uint64  `json:"mem_total_mb"`
	DiskUsedGB  uint64  `json:"disk_used_gb"`
	DiskTotalGB uint64  `json:"disk_total_gb"`
	Podman      string  `json:"podman"`     // empty when podman is not installed
	Walroos     string  `json:"walroos"`    // agent version
	Containers  int     `json:"containers"` // -1 when unknown
	// Problems the agent found on the node (agent/host/health.go): the node still works, but
	// something it runs doesn't. The UI shows each with its fix.
	Problems []agent.Problem `json:"problems,omitempty"`
}

type Node struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Roles     []string   `json:"roles"`
	Address   string     `json:"address"`
	Status    string     `json:"status"` // pending (never reported), online, offline
	Telemetry Telemetry  `json:"telemetry"`
	CreatedAt time.Time  `json:"created_at"`
	LastSeen  *time.Time `json:"last_seen"`
}

func (n *Node) setStatus(now time.Time) {
	switch {
	case n.LastSeen == nil:
		n.Status = "pending"
	case now.Sub(*n.LastSeen) > offlineAfter:
		n.Status = "offline"
	default:
		n.Status = "online"
	}
}
