// Package app is the hub's view of deployed apps: the config to build them, a queue of
// deployments run by the node's agent, and their logs. The node keeps the running release
// (a Podman Quadlet); the hub keeps the history.
package app

import (
	"errors"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
)

var (
	ErrNotFound       = errors.New("app not found")
	ErrDeployNotFound = errors.New("deployment not found")
	ErrExists         = errors.New("an app with that name already exists")
	ErrInvalid        = errors.New("name must be a DNS label, repo_url a remote git URL, port 1-65535, sha hex, branch and dockerfile without spaces or a leading '-', health_path a URL path")
	ErrNoNode         = errors.New("no online node with the prod role; join one with `walroos node join --roles prod`")
	ErrNodeRole       = errors.New("the chosen node doesn't have the prod role")
	ErrNoImage        = errors.New("only a succeeded deployment can be rolled back to")
	ErrBusy           = errors.New("a deploy of this app is running on its node; try again when it ends")
)

// App is what to build and how to check it.
type App struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	NodeID     string      `json:"node_id"`
	NodeName   string      `json:"node_name"`
	RepoURL    string      `json:"repo_url"`
	Branch     string      `json:"branch"`
	Dockerfile string      `json:"dockerfile"`
	Port       int         `json:"port"`
	HealthPath string      `json:"health_path"`
	Domains    []string    `json:"domains"` // public hostnames, through the node's Cloudflare Tunnel
	CreatedAt  time.Time   `json:"created_at"`
	Last       *Deployment `json:"last_deployment,omitempty"`
	Running    *agent.App  `json:"running,omitempty"` // the release on the node, as its agent reports it
	NodeDown   bool        `json:"node_down,omitempty"`
}

// CreateInput is what the UI or API sends. Node is a node name or ID; "" picks one.
type CreateInput struct {
	Name       string `json:"name"`
	RepoURL    string `json:"repo_url"`
	Branch     string `json:"branch"`
	Dockerfile string `json:"dockerfile"`
	Port       int    `json:"port"`
	HealthPath string `json:"health_path"`
	Node       string `json:"node"`
}

// Deployment is one run of the deploy: queued, running, then succeeded or failed.
type Deployment struct {
	ID         string     `json:"id"`
	AppID      string     `json:"app_id"`
	AppName    string     `json:"app_name,omitempty"` // only in the all-apps list
	Sha        string     `json:"sha"`
	Image      string     `json:"image"`
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	Log        string     `json:"log,omitempty"` // only when getting one deployment
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
