package mcp

import (
	"cmp"
	"context"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

// addAppTools: reading apps, their deployments and logs.
func addAppTools(s *sdk.Server, apps *app.Service) {
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

	sdk.AddTool(s, &sdk.Tool{Name: "get_deployment", Annotations: ro,
		Description: "Get a deployment's state, error and build log (its last 8000 characters)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in deploymentIn) (*sdk.CallToolResult, any, error) {
			d, err := apps.Deployment(ctx, in.Name, in.ID)
			d.Log = tail(d.Log)
			return nil, d, err
		})

	sdk.AddTool(s, &sdk.Tool{Name: "app_logs", Annotations: ro,
		Description: "The running release's output (stdout and stderr) from the node's journal."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in logsIn) (*sdk.CallToolResult, any, error) {
			logs, err := apps.Logs(ctx, in.Name, cmp.Or(in.Lines, 200))
			return nil, map[string]string{"logs": tail(logs)}, err
		})
}

// addAppActions change apps. No delete_app on purpose: it's destructive and lives in the UI.
func addAppActions(s *sdk.Server, apps *app.Service) {
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
}
