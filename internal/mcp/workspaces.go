package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

type projectIn struct {
	Name    string `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'; also the GitHub repo's name"`
	Stack   string `json:"stack" jsonschema:"one of the stacks in the tool description"`
	Version string `json:"version" jsonschema:"one of that stack's versions in the tool description"`
	GitHub  bool   `json:"github,omitempty" jsonschema:"create the repo on GitHub with the token saved in Settings; false makes a local git repo the user pushes later"`
	Private bool   `json:"private,omitempty" jsonschema:"with github: make the repo private"`
	Node    string `json:"node,omitempty" jsonschema:"node name or ID; empty picks the dev node with the fewest workspaces"`
}

// projectStacks lists what create_project accepts: "go (1.27, 1.26), node (24, 22)".
func projectStacks() string {
	var out []string
	for _, s := range agent.ProjectStacks {
		out = append(out, fmt.Sprintf("%s (%s)", s.ID, strings.Join(s.Versions, ", ")))
	}
	return strings.Join(out, "; ")
}

// addWorkspaceTools: reading workspaces and how to reach them.
func addWorkspaceTools(s *sdk.Server, ws *workspace.Service) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_workspaces", Annotations: ro,
		Description: "List dev workspaces with their node, repo, branch and state (creating, running, stopped, failed)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := ws.List(ctx)
			return nil, map[string]any{"workspaces": orEmpty(list)}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "get_workspace", Annotations: ro,
		Description: "Get one workspace with its current state, refreshed from its node."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Get(ctx, in.Name)
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
}

// addWorkspaceActions create workspaces as u, who owns them.
func addWorkspaceActions(s *sdk.Server, ws *workspace.Service, u auth.User) {
	login := u.Login
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

	sdk.AddTool(s, &sdk.Tool{Name: "create_workspace_from_scratch",
		Description: "Start a new project that has no repo yet: a workspace whose repo has a .devcontainer/devcontainer.json for the stack and version, " +
			"and a README. With github=true the hub creates the repo on GitHub and commits them (needs a token in Settings); " +
			"else the node runs git init and stages them, and the user adds origin later. Stacks and versions: " + projectStacks() + ". " +
			"Returns in state 'creating'; poll get_workspace like create_workspace."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in projectIn) (*sdk.CallToolResult, any, error) {
			if in.GitHub {
				if err := u.Check(auth.PermSettings); err != nil {
					return nil, nil, err
				}
			}
			w, err := ws.CreateProject(ctx, login, workspace.ProjectInput{Name: in.Name, Stack: in.Stack, Version: in.Version,
				GitHub: in.GitHub, Private: in.Private, Node: in.Node})
			return nil, w, err
		})

}

// addWorkspaceLifecycle starts and stops workspaces u may act on.
func addWorkspaceLifecycle(s *sdk.Server, ws *workspace.Service, u auth.User) {
	sdk.AddTool(s, &sdk.Tool{Name: "start_workspace", Annotations: &sdk.ToolAnnotations{IdempotentHint: true},
		Description: "Start a stopped workspace."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			if err := ws.Authorize(ctx, u, in.Name); err != nil {
				return nil, nil, err
			}
			w, err := ws.Start(ctx, in.Name)
			return nil, w, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "stop_workspace", Annotations: &sdk.ToolAnnotations{IdempotentHint: true},
		Description: "Stop a workspace. Files in the repo and $HOME are kept."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			if err := ws.Authorize(ctx, u, in.Name); err != nil {
				return nil, nil, err
			}
			w, err := ws.Stop(ctx, in.Name)
			return nil, w, err
		})
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
		"all_hosts":  "walroos ssh-config --hub <hub> --write  # every workspace, kept in a walroos section of ~/.ssh/config",
	}
}
