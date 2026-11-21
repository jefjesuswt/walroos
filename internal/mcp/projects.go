package mcp

import (
	"context"

	"github.com/jefjesuswt/walroos/internal/project"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
