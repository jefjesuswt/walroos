// Package agent is the contract between the hub and the agent on each node: the specs the hub
// sends (WorkspaceSpec, AppSpec, DatabaseSpec), what nodes answer (Workspace, Release, Database),
// how names and values are checked, the ports, and the Client the hub calls nodes with.
//
// The node side lives in the subpackages: server (the HTTP API), workspaces, apps, databases,
// sshgw (the SSH gateway), quadlet (systemd units, locks, egress guard) and host (the CLIs).
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"tailscale.com/client/local"
)

// Port is where node agents listen for the hub.
const Port = "7070"

// Info is what GET /v1/info returns.
type Info struct {
	Hostname string `json:"hostname"`
	Walroos  string `json:"walroos"`
	Podman   string `json:"podman"` // empty when podman is not installed
}

// Identify returns the tailnet StableNodeID of the peer at remoteAddr (ip:port).
type Identify func(ctx context.Context, remoteAddr string) (string, error)

// Tailscale identifies callers with the host tailscaled's WhoIs. In userspace mode
// (--tun=userspace-networking) tailnet traffic arrives on loopback and WhoIs still resolves it.
func Tailscale(lc *local.Client) Identify {
	return func(ctx context.Context, remoteAddr string) (string, error) {
		who, err := lc.WhoIs(ctx, remoteAddr)
		if err != nil {
			return "", err
		}
		return string(who.Node.StableID), nil
	}
}

// TokenHash is how node tokens are stored on the hub and presented to the agent.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// DeployLine is one line of a deploy's NDJSON answer: log output while it runs, then one
// line with Release or Error.
type DeployLine struct {
	Log     string   `json:"log,omitempty"`
	Release *Release `json:"release,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// SSHPort is where node agents serve SSH for workspaces.
const SSHPort = "2222"
