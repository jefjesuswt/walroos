// Package workspace is the hub's view of dev workspaces: it picks a node, calls the node's
// agent, and keeps the row the UI reads. Podman on the node stays the source of truth for the
// container; the row mirrors it and is refreshed from the agent.
package workspace

import (
	"encoding/json"
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
	ErrRepo      = errors.New("the node couldn't read the repo")
	ErrRepoURL   = errors.New("repo_url must be a remote git URL: https://, ssh://, git:// or user@host:path")
	ErrStack     = errors.New("stack must be one detect returns: android, dotnet, bun, node, go, rust, python, java, devcontainer or none")
)

// stacks are the ones worth storing; detect's "devcontainer" and "none" are stored as "".
var stacks = map[string]bool{"android": true, "dotnet": true, "bun": true, "node": true, "go": true, "rust": true, "python": true, "java": true}

// normalizeStack checks a stack from the API and returns what to store.
func normalizeStack(s string) (string, error) {
	switch {
	case s == "" || s == "devcontainer" || s == "none":
		return "", nil
	case stacks[s]:
		return s, nil
	}
	return "", ErrStack
}

// Workspace mirrors a workspace on a node.
type Workspace struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Owner            string    `json:"owner"`
	NodeID           string    `json:"node_id"`
	NodeName         string    `json:"node_name"`
	NodeAddress      string    `json:"node_address,omitempty"` // tailnet IP; what ssh-config dials
	RepoURL          string    `json:"repo_url"`
	Branch           string    `json:"branch"`
	DevcontainerPath string    `json:"devcontainer_path"`
	Image            string    `json:"image,omitempty"`
	Stack            string    `json:"stack,omitempty"`  // detected at create time; "" = unknown
	Folder           string    `json:"folder,omitempty"` // repo path inside the container, from the node (not stored)
	State            string    `json:"state"`            // creating, running, stopped, failed, deleting
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
	// Stack is what detect found (android, go...), for display. "devcontainer" and "none" are
	// stored as "" (the image says more there).
	Stack string `json:"stack,omitempty"`
	// Devcontainer is a devcontainer.json to use instead of the repo's (the one detect generates).
	// The node keeps it with the workspace for rebuilds; the hub doesn't store it.
	Devcontainer json.RawMessage `json:"devcontainer,omitempty"`
}

// DetectInput asks a node to propose a devcontainer for a repo. Node is a name or ID; "" picks one.
type DetectInput struct {
	RepoURL string `json:"repo_url"`
	Branch  string `json:"branch"`
	Node    string `json:"node"`
}

// EnvVar is one environment variable of a workspace. Secret only affects how the UI shows it;
// all values are encrypted at rest either way.
type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}
