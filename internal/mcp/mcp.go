// Package mcp serves the hub's MCP endpoint (streamable HTTP): tools over the same services the
// API uses, so an agent like Claude Code can list nodes and create and reach workspaces.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler serves /mcp. Mount it behind user auth: it trusts auth.FromContext for the owner.
// Stateless: every request gets a server bound to the caller, no sessions to keep.
func Handler(nodes *fleet.Service, ws *workspace.Service) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		u, _ := auth.FromContext(r.Context())
		return newServer(nodes, ws, u.Login)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

type nameIn struct {
	Name string `json:"name" jsonschema:"workspace name"`
}

type detectIn struct {
	RepoURL string `json:"repo_url" jsonschema:"git URL (https or git@host:owner/repo); public repos only for now"`
	Branch  string `json:"branch,omitempty" jsonschema:"branch to look at; default branch if empty"`
	Node    string `json:"node,omitempty" jsonschema:"node name or ID; empty picks the dev node with the fewest workspaces"`
}

type createIn struct {
	Name             string         `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'"`
	RepoURL          string         `json:"repo_url" jsonschema:"git URL (https or git@host:owner/repo); public repos only for now"`
	Branch           string         `json:"branch,omitempty" jsonschema:"branch to clone; default branch if empty"`
	Node             string         `json:"node,omitempty" jsonschema:"node name or ID; empty picks the dev node with the fewest workspaces"`
	DevcontainerPath string         `json:"devcontainer_path,omitempty" jsonschema:"path of the repo's devcontainer.json, if not in a standard location"`
	Devcontainer     map[string]any `json:"devcontainer,omitempty" jsonschema:"devcontainer.json to use instead of the repo's, e.g. the one detect_stack proposes"`
}

func newServer(nodes *fleet.Service, ws *workspace.Service, login string) *sdk.Server {
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
				Node: in.Node, DevcontainerPath: in.DevcontainerPath, Devcontainer: dc})
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

	return s
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
