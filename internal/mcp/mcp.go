// Package mcp serves the hub's MCP endpoint (streamable HTTP): tools over the same services the
// API uses, so an agent like Claude Code can list nodes, create and reach workspaces, and deploy apps.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler serves /mcp. Mount it behind user auth: it trusts auth.FromContext for the owner.
// Stateless: every request gets a server bound to the caller, no sessions to keep.
func Handler(nodes *fleet.Service, ws *workspace.Service, apps *app.Service) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		u, _ := auth.FromContext(r.Context())
		return newServer(nodes, ws, apps, u.Login)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

type nameIn struct {
	Name string `json:"name" jsonschema:"workspace name"`
}

type detectIn struct {
	RepoURL string `json:"repo_url" jsonschema:"git URL; for a private repo use the SSH form git@host:owner/repo, which the node clones with its own SSH keys"`
	Branch  string `json:"branch,omitempty" jsonschema:"branch to look at; default branch if empty"`
	Node    string `json:"node,omitempty" jsonschema:"node name or ID; empty picks the dev node with the fewest workspaces"`
}

type createIn struct {
	Name             string         `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'"`
	RepoURL          string         `json:"repo_url" jsonschema:"git URL; for a private repo use the SSH form git@host:owner/repo, which the node clones with its own SSH keys"`
	Branch           string         `json:"branch,omitempty" jsonschema:"branch to clone; default branch if empty"`
	Node             string         `json:"node,omitempty" jsonschema:"node name or ID; empty picks the dev node with the fewest workspaces"`
	DevcontainerPath string         `json:"devcontainer_path,omitempty" jsonschema:"path of the repo's devcontainer.json, if not in a standard location"`
	Devcontainer     map[string]any `json:"devcontainer,omitempty" jsonschema:"devcontainer.json to use instead of the repo's, e.g. the one detect_stack proposes"`
	Stack            string         `json:"stack,omitempty" jsonschema:"the stack detect_stack returned (android, go, dotnet...), shown in the UI"`
}

func newServer(nodes *fleet.Service, ws *workspace.Service, apps *app.Service, login string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "walroos", Version: version.Version}, nil)
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}

	sdk.AddTool(s, &sdk.Tool{Name: "list_nodes", Annotations: ro,
		Description: "List the machines joined to the hub: roles (dev runs workspaces), status (online/offline) and telemetry (CPU, memory, disk, Podman)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := nodes.List(ctx)
			return nil, map[string]any{"nodes": orEmpty(list)}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "list_workspaces", Annotations: ro,
		Description: "List dev workspaces with their node, repo, branch and state (creating, running, stopped, failed)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := ws.List(ctx)
			return nil, map[string]any{"workspaces": orEmpty(list)}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "detect_stack", Annotations: ro,
		Description: "Shallow-clone a repo on a dev node and detect its stack (Go, Node/Bun, Python, .NET, Java, Android, Rust...). " +
			"Returns the tools found and a proposed devcontainer to pass to create_workspace. Nothing is kept."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in detectIn) (*sdk.CallToolResult, any, error) {
			raw, err := ws.Detect(ctx, workspace.DetectInput(in))
			if err != nil {
				return nil, nil, err
			}
			var d map[string]any
			return nil, d, json.Unmarshal(raw, &d)
		})

	sdk.AddTool(s, &sdk.Tool{Name: "create_workspace",
		Description: "Create a dev workspace from a git repo on a dev node. Returns right away in state 'creating'; " +
			"poll get_workspace until 'running' (or 'failed', with the error). Then connect with workspace_ssh_info."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in createIn) (*sdk.CallToolResult, any, error) {
			var dc json.RawMessage
			if in.Devcontainer != nil {
				dc, _ = json.Marshal(in.Devcontainer)
			}
			w, err := ws.Create(ctx, login, workspace.CreateInput{Name: in.Name, RepoURL: in.RepoURL, Branch: in.Branch,
				Node: in.Node, DevcontainerPath: in.DevcontainerPath, Devcontainer: dc, Stack: in.Stack})
			return nil, w, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "get_workspace", Annotations: ro,
		Description: "Get one workspace with its current state, refreshed from its node."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Get(ctx, in.Name)
			return nil, w, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "start_workspace", Annotations: &sdk.ToolAnnotations{IdempotentHint: true},
		Description: "Start a stopped workspace."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Start(ctx, in.Name)
			return nil, w, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "stop_workspace", Annotations: &sdk.ToolAnnotations{IdempotentHint: true},
		Description: "Stop a workspace. Files in the repo and $HOME are kept."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Stop(ctx, in.Name)
			return nil, w, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "workspace_ssh_info", Annotations: ro,
		Description: "How to SSH into a workspace: an ~/.ssh/config block and a one-off ssh command. " +
			"The caller's SSH public key must be registered in the hub (Settings → SSH keys). A stopped workspace starts on connect."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Get(ctx, in.Name)
			if err != nil {
				return nil, nil, err
			}
			return nil, sshInfo(w), nil
		})

	addAppTools(s, apps)
	return s
}

type domainsIn struct {
	Name    string   `json:"name" jsonschema:"app name"`
	Domains []string `json:"domains" jsonschema:"hostnames like app.example.com"`
}

type appIn struct {
	Name string `json:"name" jsonschema:"app name"`
}

type createAppIn struct {
	Name       string `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'"`
	RepoURL    string `json:"repo_url" jsonschema:"git URL of a repo with a Dockerfile (https or git@host:owner/repo)"`
	Port       int    `json:"port" jsonschema:"port the container listens on"`
	Branch     string `json:"branch,omitempty" jsonschema:"branch to build; default branch if empty"`
	Dockerfile string `json:"dockerfile,omitempty" jsonschema:"path of the Dockerfile in the repo; Dockerfile if empty"`
	HealthPath string `json:"health_path,omitempty" jsonschema:"URL path that must answer 2xx/3xx within 90 s; / if empty"`
	Node       string `json:"node,omitempty" jsonschema:"node name or ID with the prod role; empty picks the first online one"`
}

type deployIn struct {
	Name string `json:"name" jsonschema:"app name"`
	Sha  string `json:"sha,omitempty" jsonschema:"commit to deploy (7 to 40 hex); empty deploys the branch head"`
}

type deploymentIn struct {
	Name string `json:"name" jsonschema:"app name"`
	ID   string `json:"id" jsonschema:"deployment ID"`
}

type logsIn struct {
	Name  string `json:"name" jsonschema:"app name"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many of the last lines; 200 if empty"`
}

// logTail bounds what goes back to the model: a build log can be a megabyte.
const logTail = 8000

func tail(s string) string {
	if len(s) > logTail {
		return "…" + s[len(s)-logTail:]
	}
	return s
}

// addAppTools: no delete_app on purpose; it's destructive and lives in the UI.
func addAppTools(s *sdk.Server, apps *app.Service) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}

	sdk.AddTool(s, &sdk.Tool{Name: "list_apps", Annotations: ro,
		Description: "List deployed apps: node, repo, port, the last deployment and the release the node is running (`running`; absent if none)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := apps.List(ctx)
			return nil, map[string]any{"apps": orEmpty(list)}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "get_app", Annotations: ro,
		Description: "Get one app with its running release and its recent deployments (newest first, without logs)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in appIn) (*sdk.CallToolResult, any, error) {
			a, err := apps.Get(ctx, in.Name)
			if err != nil {
				return nil, nil, err
			}
			deps, err := apps.Deployments(ctx, in.Name)
			return nil, map[string]any{"app": a, "deployments": deps[:min(len(deps), 10)]}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "create_app",
		Description: "Create an app from a git repo with a Dockerfile, on a node with the prod role. Nothing runs until deploy_app."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in createAppIn) (*sdk.CallToolResult, any, error) {
			a, err := apps.Create(ctx, app.CreateInput{Name: in.Name, RepoURL: in.RepoURL, Branch: in.Branch, Dockerfile: in.Dockerfile,
				Port: in.Port, HealthPath: in.HealthPath, Node: in.Node})
			return nil, a, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "deploy_app",
		Description: "Queue a deploy: build on the node, start next to the running release, health check, then stop the old one. " +
			"A release that never gets healthy is removed and the old one keeps serving. Returns the queued deployment; poll get_deployment until succeeded or failed."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in deployIn) (*sdk.CallToolResult, any, error) {
			d, err := apps.Deploy(ctx, in.Name, in.Sha)
			return nil, d, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "get_deployment", Annotations: ro,
		Description: "Get a deployment's state, error and build log (its last 8000 characters)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in deploymentIn) (*sdk.CallToolResult, any, error) {
			d, err := apps.Deployment(ctx, in.Name, in.ID)
			d.Log = tail(d.Log)
			return nil, d, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "rollback_app",
		Description: "Queue a deploy that runs the image of an earlier succeeded deployment, with no build. Poll get_deployment like a deploy."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in deploymentIn) (*sdk.CallToolResult, any, error) {
			d, err := apps.Rollback(ctx, in.Name, in.ID)
			return nil, d, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "set_app_domains",
		Description: "Make exactly these hostnames public for the app, with TLS, through its node's Cloudflare Tunnel (an empty list removes them). Needs a Cloudflare token in Settings; each hostname must be in one of its zones."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in domainsIn) (*sdk.CallToolResult, any, error) {
			a, err := apps.SetDomains(ctx, in.Name, in.Domains)
			return nil, a, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "app_logs", Annotations: ro,
		Description: "The running release's output (stdout and stderr) from the node's journal."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in logsIn) (*sdk.CallToolResult, any, error) {
			logs, err := apps.Logs(ctx, in.Name, cmpOr(in.Lines, 200))
			return nil, map[string]string{"logs": tail(logs)}, err
		})
}

func cmpOr(n, fallback int) int {
	if n <= 0 {
		return fallback
	}
	return min(n, 2000)
}

// sshInfo matches what `walroos ssh-config` prints for one workspace.
func sshInfo(w workspace.Workspace) map[string]string {
	host := w.NodeAddress
	if host == "" {
		host = w.NodeName
	}
	alias := w.Name + ".walroos"
	return map[string]string{
		"state":      w.State,
		"host":       alias,
		"ssh_config": fmt.Sprintf("Host %s\n  HostName %s\n  Port %s\n  User %s\n", alias, host, agent.SSHPort, w.Name),
		"command":    fmt.Sprintf("ssh -p %s %s@%s", agent.SSHPort, w.Name, host),
		"all_hosts":  "walroos ssh-config >> ~/.ssh/config  # every workspace at once",
	}
}

// orEmpty makes a nil list encode as [] rather than null.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
