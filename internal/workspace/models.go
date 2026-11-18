// Package workspace is the hub's view of dev workspaces: it picks a node, calls the node's
// agent, and keeps the row the UI reads. Podman on the node stays the source of truth for the
// container; the row mirrors it and is refreshed from the agent.
package workspace

import (
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("workspace not found")
	ErrExists    = errors.New("a workspace with that name already exists")
	ErrInvalid   = errors.New("name must be a DNS label (lowercase letters, digits, '-'), and repo_url is required")
	ErrNoNode    = errors.New("no online node with the dev role; join one with `walroos node join --roles dev`")
	ErrNodeRole  = errors.New("the chosen node doesn't have the dev role")
	ErrAgentBusy = errors.New("the node is busy with that workspace, try again in a moment")
)

// Workspace mirrors a workspace on a node.
type Workspace struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Owner            string    `json:"owner"`
	NodeID           string    `json:"node_id"`
	NodeName         string    `json:"node_name"`
	RepoURL          string    `json:"repo_url"`
	Branch           string    `json:"branch"`
	DevcontainerPath string    `json:"devcontainer_path"`
	Image            string    `json:"image,omitempty"`
	State            string    `json:"state"` // creating, running, stopped, failed, deleting
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreateInput is what the UI or API sends. Node is a node name or ID; "" picks one.
type CreateInput struct {
	Name             string `json:"name"`
	RepoURL          string `json:"repo_url"`
	Branch           string `json:"branch"`
	DevcontainerPath string `json:"devcontainer_path"`
	Node             string `json:"node"`
}
