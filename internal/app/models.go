// Package app is the hub's view of deployed apps: the config to build them, a queue of
// deployments run by the node's agent, and their logs. The node keeps the running release
// (a Podman Quadlet); the hub keeps the history.
package app

import (
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNotFound       = whttp.NotFound("app not found")
	ErrDeployNotFound = whttp.NotFound("deployment not found")
	ErrExists         = whttp.Conflict("an app with that name already exists")
	ErrInvalid        = whttp.Invalid("name must be a DNS label, repo_url a remote git URL or image a fully qualified image (registry.example.com/repo:tag) but not both, port 1-65535, sha hex, branch and dockerfile without spaces or a leading '-', health_path a URL path; a static site comes from a repo, with no dockerfile, runtime node or bun and a relative publish folder")
	ErrNoNode         = whttp.Conflict("no online node with the prod role; join one with `walroos node join --roles prod`")
	ErrNodeRole       = whttp.Conflict("the chosen node doesn't have the prod role")
	ErrNoImage        = whttp.Conflict("only a succeeded deployment can be rolled back to")
	ErrVolumes        = whttp.Invalid("each volume needs a name (lowercase, digits, '_.-') or a host_path inside the node's ~/walroos-data, and an absolute target; no two targets alike, 16 at most")
	ErrBusy           = whttp.Conflict("a deploy of this app is running on its node; try again when it ends")
	ErrNoSource       = whttp.Invalid("the app has no source yet: set a repository or an image in its General tab")
	ErrPower          = whttp.Invalid("action must be stop, start or restart")
	ErrNoPort         = whttp.Invalid("the app has no port (it's checked by running): a domain needs a port it serves on")
	ErrNotDeployed    = whttp.Conflict("the app has no release on its node yet: deploy it first")
)

// App is what to build and how to check it.
type App struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	NodeID      string             `json:"node_id"`
	NodeName    string             `json:"node_name"`
	ProjectID   string             `json:"project_id"`
	Project     string             `json:"project"` // its name; the project's network is the app's
	Description string             `json:"description"`
	Compose     string             `json:"compose,omitempty"` // the compose service it comes from (its alias on the network); "" if none
	RepoURL     string             `json:"repo_url"`          // RepoURL and Image both "": a draft, created before its source
	Image       string             `json:"image"`             // a published image, instead of RepoURL: pulled on each deploy
	Branch      string             `json:"branch"`
	Dockerfile  string             `json:"dockerfile"`
	Static      *agent.StaticBuild `json:"static,omitempty"`  // a static site: built with Node or Bun, no Dockerfile
	Health      string             `json:"health,omitempty"`  // http (""), tcp or running: agent.Health*
	Command     []string           `json:"command,omitempty"` // replaces the image's CMD
	Context     string             `json:"context,omitempty"` // the repo folder to build from; the Dockerfile is relative to it
	Port        int                `json:"port"`
	HealthPath  string             `json:"health_path"`
	Domains     []string           `json:"domains"`      // public hostnames, through the node's Cloudflare Tunnel
	Volumes     []agent.Volume     `json:"volumes"`      // apply on the next deploy
	Trigger     string             `json:"trigger"`      // manual, on_push or on_ci (triggers.go)
	DeployToken bool               `json:"deploy_token"` // whether CI has a deploy token; the token is shown once
	CreatedAt   time.Time          `json:"created_at"`
	Last        *Deployment        `json:"last_deployment,omitempty"`
	Running     *agent.App         `json:"running,omitempty"` // the release on the node, as its agent reports it
	NodeDown    bool               `json:"node_down,omitempty"`
}

// CreateInput is what the UI or API sends. Node is a node name or ID; "" picks one.
type CreateInput struct {
	Name       string             `json:"name"`
	RepoURL    string             `json:"repo_url"`
	Image      string             `json:"image"` // or a published image; then no branch, dockerfile or build
	Branch     string             `json:"branch"`
	Dockerfile string             `json:"dockerfile"`
	Static     *agent.StaticBuild `json:"static,omitempty"`  // a repo with no Dockerfile: build it as a static site; port is then agent.StaticPort
	Health     string             `json:"health,omitempty"`  // http (""), tcp or running: agent.Health*
	Command    []string           `json:"command,omitempty"` // replaces the image's CMD
	Context    string             `json:"context,omitempty"` // the repo folder to build from; the Dockerfile is relative to it
	Port       int                `json:"port"`
	HealthPath string             `json:"health_path"`
	Node       string             `json:"node"`
	Project    string             `json:"project"` // name; "" = default
	// Description is shown on the project's card. With no repo_url, image and port the app is a
	// draft: SetSource gives it one later, like Dokploy's create-then-configure.
	Description string `json:"description"`
	Compose     string `json:"compose,omitempty"` // set by a compose sync: the service it comes from
}

// SourceInput is what to build or pull, and how to check it: Create's source fields.
type SourceInput struct {
	RepoURL    string             `json:"repo_url"`
	Image      string             `json:"image"`
	Branch     string             `json:"branch"`
	Dockerfile string             `json:"dockerfile"`
	Static     *agent.StaticBuild `json:"static,omitempty"`
	Health     string             `json:"health,omitempty"`  // http (""), tcp or running: agent.Health*
	Command    []string           `json:"command,omitempty"` // replaces the image's CMD
	Context    string             `json:"context,omitempty"` // the repo folder to build from; the Dockerfile is relative to it
	Port       int                `json:"port"`
	HealthPath string             `json:"health_path"`
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
