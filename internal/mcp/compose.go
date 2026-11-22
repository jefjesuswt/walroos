package mcp

import (
	"context"

	"github.com/jefjesuswt/walroos/internal/compose"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type composeIn struct {
	Project string `json:"project" jsonschema:"the project the services go in (not default; create_project first)"`
	RepoURL string `json:"repo_url,omitempty" jsonschema:"repo holding the compose file (its build: contexts build from it); or set text"`
	Branch  string `json:"branch,omitempty" jsonschema:"branch of the repo; its default if empty"`
	Path    string `json:"path,omitempty" jsonschema:"the compose file's path in the repo; docker-compose.yml if empty"`
	Text    string `json:"text,omitempty" jsonschema:"a pasted compose file instead of a repo (published images only, no build)"`
}

func (in composeIn) source() compose.Source {
	return compose.Source{RepoURL: in.RepoURL, Branch: in.Branch, Path: in.Path, Text: in.Text}
}

// addComposeTools: a docker-compose.yml as a project's apps. Each service becomes the app
// <project>-<service>, reachable by its service name on the project's network.
func addComposeTools(s *sdk.Server, c *compose.Service) {
	if c == nil {
		return
	}
	sdk.AddTool(s, &sdk.Tool{Name: "plan_compose", Annotations: ro,
		Description: "Read a compose file and say what sync_compose would do: the apps it creates, updates and removes, in deploy order, and keys it ignores. Changes nothing. Supported: image, build (context, dockerfile), command, environment, env_file, ports/expose (one TCP port each), named volumes, depends_on; ${VAR} comes from the project's shared variables and the repo's .env."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in composeIn) (*sdk.CallToolResult, any, error) {
			d, err := c.Plan(ctx, in.Project, in.source())
			return nil, d, err
		})
	sdk.AddTool(s, &sdk.Tool{Name: "sync_compose",
		Description: "Sync a project with a compose file: create, update and remove its services' apps (removed ones keep their volumes), save it as the project's compose source and deploy every service in depends_on order. Returns the deployments; poll get_deployment."},
		func(ctx context.Context, _ *sdk.CallToolRequest, in composeIn) (*sdk.CallToolResult, any, error) {
			d, err := c.Apply(ctx, in.Project, in.source())
			return nil, d, err
		})
}
