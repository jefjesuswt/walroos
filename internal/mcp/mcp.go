// Package mcp serves the hub's MCP endpoint (streamable HTTP): tools over the same services the
// API uses, so an agent like Claude Code can list nodes, create and reach workspaces, and deploy apps.
package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler serves /mcp. Mount it behind user auth: it trusts auth.FromContext for the owner.
// Stateless: every request gets a server bound to the caller, no sessions to keep.
func Handler(nodes *fleet.Service, ws *workspace.Service, apps *app.Service, dbs *database.Service, projects *project.Service) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		u, _ := auth.FromContext(r.Context())
		return newServer(nodes, ws, apps, dbs, projects, u)
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

// newServer offers u only the tools u's role may use: a tool the model never sees is one it
// can't be talked into calling. Calls that depend on what they touch check it themselves.
func newServer(nodes *fleet.Service, ws *workspace.Service, apps *app.Service, dbs *database.Service, projects *project.Service, u auth.User) *sdk.Server {
	login := u.Login
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

	sdk.AddTool(s, &sdk.Tool{Name: "get_workspace", Annotations: ro,
		Description: "Get one workspace with its current state, refreshed from its node."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in nameIn) (*sdk.CallToolResult, any, error) {
			w, err := ws.Get(ctx, in.Name)
			return nil, w, err
		})

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

	addProjectTools(s, projects, u.Can(auth.PermApps))
	addAppTools(s, apps)
	if !u.Can(auth.PermApps) { // reading apps and logs stays
		s.RemoveTools("create_app", "deploy_app", "rollback_app", "set_app_domains", "set_app_volumes")
	}
	if u.Can(auth.PermDatabases) {
		addDatabaseTools(s, dbs)
	}
	return s
}

type volumesIn struct {
	Name    string         `json:"name" jsonschema:"app name"`
	Volumes []agent.Volume `json:"volumes" jsonschema:"each: name (a podman volume, walroos-app-<app>-<name>) or host_path (a directory relative to the node's ~/walroos-data), target (absolute path in the container), ro"`
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
	RepoURL    string `json:"repo_url,omitempty" jsonschema:"git URL of a repo with a Dockerfile (https or git@host:owner/repo); or set image instead"`
	Image      string `json:"image,omitempty" jsonschema:"a published image instead of a repo, fully qualified (ghcr.io/owner/app:tag); pulled again on each deploy"`
	Port       int    `json:"port" jsonschema:"port the container listens on"`
	Branch     string `json:"branch,omitempty" jsonschema:"branch to build; default branch if empty"`
	Dockerfile string `json:"dockerfile,omitempty" jsonschema:"path of the Dockerfile in the repo; Dockerfile if empty"`
	HealthPath string `json:"health_path,omitempty" jsonschema:"URL path that must answer 2xx/3xx within 90 s; / if empty"`
	Node       string `json:"node,omitempty" jsonschema:"node name or ID with the prod role; empty picks the first online one"`
	Project    string `json:"project,omitempty" jsonschema:"project the app goes in (see list_projects); empty is the default project"`
}

type deployIn struct {
	Name string `json:"name" jsonschema:"app name"`
	Sha  string `json:"sha,omitempty" jsonschema:"commit to deploy (7 to 40 hex); empty deploys the branch head, or pulls an image app's tag again"`
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
		Description: "Create an app from a git repo with a Dockerfile, or from a published image, on a node with the prod role. Nothing runs until deploy_app."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in createAppIn) (*sdk.CallToolResult, any, error) {
			a, err := apps.Create(ctx, app.CreateInput{Name: in.Name, RepoURL: in.RepoURL, Image: in.Image, Branch: in.Branch, Dockerfile: in.Dockerfile,
				Port: in.Port, HealthPath: in.HealthPath, Node: in.Node, Project: in.Project})
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

	sdk.AddTool(s, &sdk.Tool{Name: "set_app_volumes",
		Description: "Replace the app's persistent volumes (an empty list removes them from the app; their data stays on the node). They apply on the next deploy_app. An app with volumes deploys by stopping the old release first, so two never share the data."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in volumesIn) (*sdk.CallToolResult, any, error) {
			a, err := apps.SetVolumes(ctx, in.Name, in.Volumes)
			return nil, a, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "app_logs", Annotations: ro,
		Description: "The running release's output (stdout and stderr) from the node's journal."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in logsIn) (*sdk.CallToolResult, any, error) {
			logs, err := apps.Logs(ctx, in.Name, cmp.Or(in.Lines, 200))
			return nil, map[string]string{"logs": tail(logs)}, err
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

// orEmpty makes a nil list encode as [] rather than null.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

type databaseIn struct {
	Name string `json:"name" jsonschema:"database name"`
}

type createDatabaseIn struct {
	Name    string `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'"`
	Engine  string `json:"engine" jsonschema:"one of the engines in the tool description"`
	Version string `json:"version" jsonschema:"one of that engine's versions in the tool description"`
	Node    string `json:"node,omitempty" jsonschema:"prod node name or ID; empty picks an online one"`
	Project string `json:"project,omitempty" jsonschema:"project the database goes in (see list_projects); empty is the default project"`
}

type linkDatabaseIn struct {
	Name string `json:"name" jsonschema:"database name"`
	App  string `json:"app" jsonschema:"app that gets the connection URL; it must run on the database's node"`
	Key  string `json:"key,omitempty" jsonschema:"env variable; DATABASE_URL (REDIS_URL for redis) if empty"`
}

// databaseEngines lists what create_database accepts: "postgres (18, 17, 16); redis (8, 7)".
func databaseEngines() string {
	var out []string
	for _, e := range database.Engines() {
		out = append(out, fmt.Sprintf("%s (%s)", e.ID, strings.Join(e.Versions, ", ")))
	}
	return strings.Join(out, "; ")
}

// addDatabaseTools: no delete_database or restore on purpose; both destroy data and live in the UI.
func addDatabaseTools(s *sdk.Server, dbs *database.Service) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}

	sdk.AddTool(s, &sdk.Tool{Name: "list_databases", Annotations: ro,
		Description: "List database services: engine, version, node, host and port on the node's walroos network, state, backup schedule and last backup. No passwords."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := dbs.List(ctx)
			return nil, map[string]any{"databases": orEmpty(list)}, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "create_database",
		Description: "Create a database service on a prod node, with a generated password and daily backups (7 kept). Engines and versions: " +
			databaseEngines() + ". Apps on the same node reach it by its host name; link_database puts its URL in an app's env."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in createDatabaseIn) (*sdk.CallToolResult, any, error) {
			d, err := dbs.Create(ctx, database.CreateInput{Name: in.Name, Engine: in.Engine, Version: in.Version, Node: in.Node, Project: in.Project})
			d.URL = "" // the password stays out of the model's context
			return nil, d, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "link_database",
		Description: "Put the database's connection URL (with its password) in an app's environment as a secret variable. It applies on the app's next deploy_app."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in linkDatabaseIn) (*sdk.CallToolResult, any, error) {
			vars, err := dbs.Link(ctx, in.Name, database.LinkInput{App: in.App, Key: in.Key})
			keys := []string{}
			for _, v := range vars {
				keys = append(keys, v.Key)
			}
			return nil, map[string]any{"env_keys": keys}, err // keys only: values hold secrets
		})

	sdk.AddTool(s, &sdk.Tool{Name: "backup_database",
		Description: "Dump the database now to a gzip file on its node; older dumps beyond its keep count are removed."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in databaseIn) (*sdk.CallToolResult, any, error) {
			b, err := dbs.Backup(ctx, in.Name)
			return nil, b, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "list_database_backups", Annotations: ro,
		Description: "List a database's dumps on its node, newest first. Restoring one is done from the UI."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in databaseIn) (*sdk.CallToolResult, any, error) {
			list, err := dbs.Backups(ctx, in.Name)
			return nil, map[string]any{"backups": orEmpty(list)}, err
		})
}

type projectNameIn struct {
	Name string `json:"name" jsonschema:"project name"`
}

type createProjectIn struct {
	Name        string `json:"name" jsonschema:"DNS label: lowercase letters, digits and '-'"`
	Description string `json:"description,omitempty" jsonschema:"what the project is, shown in the UI"`
}

// addProjectTools: projects group apps and databases; services of one project reach each other by
// name on its own network. No delete_project: it lives in the UI.
func addProjectTools(s *sdk.Server, projects *project.Service, canCreate bool) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}
	sdk.AddTool(s, &sdk.Tool{Name: "list_projects", Annotations: ro,
		Description: "List projects with their services (apps and databases) and each one's state. Services of a project share a network on their node and reach each other by service name."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := projects.List(ctx)
			return nil, map[string]any{"projects": orEmpty(list)}, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "get_project", Annotations: ro,
		Description: "Get one project with its apps and databases."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in projectNameIn) (*sdk.CallToolResult, any, error) {
			p, err := projects.Get(ctx, in.Name)
			return nil, p, err
		})
	if !canCreate {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "create_project",
		Description: "Create an empty project. Then create_app and create_database with project set put services in it; they reach each other by name (an app named api is http://api:<port> to the others)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in createProjectIn) (*sdk.CallToolResult, any, error) {
			p, err := projects.Create(ctx, project.CreateInput{Name: in.Name, Description: in.Description})
			return nil, p, err
		})
}
