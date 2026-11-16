package fleet

import (
	"errors"
	"time"
)

var (
	ErrNodeNotFound          = errors.New("node not found")
	ErrNodeNameAlreadyExists = errors.New("node name already exists")
	ErrInvalidJoinToken      = errors.New("join token is invalid, expired or already used")
	ErrUnauthorized          = errors.New("invalid node token")
	ErrInvalidName           = errors.New("node name must be a DNS label: lowercase letters, digits and '-', max 63 chars")
	ErrInvalidRoles          = errors.New("roles must be one or more of: dev, build, prod")
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
