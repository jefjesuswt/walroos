// Package mcp serves the hub's MCP endpoint (streamable HTTP): tools over the same services the
// API uses, so an agent like Claude Code can list nodes, create and reach workspaces, and deploy apps.
package mcp

import (
	"context"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/compose"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/internal/workspace"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler serves /mcp. Mount it behind user auth: it trusts auth.FromContext for the owner.
// Stateless: every request gets a server bound to the caller, no sessions to keep.
// Services are the domains MCP's tools call.
type Services struct {
	Nodes      *fleet.Service
	Workspaces *workspace.Service
	Apps       *app.Service
	Databases  *database.Service
	Projects   *project.Service
	Compose    *compose.Service
}

func Handler(svc Services) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		u, _ := auth.FromContext(r.Context())
		return newServer(svc, u)
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
}

// newServer offers u only the tools u's role may use: a tool the model never sees is one it
// can't be talked into calling. Calls that depend on what they touch check it themselves.
func newServer(svc Services, u auth.User) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "walroos", Version: version.Version}, nil)
	addNodeTools(s, svc.Nodes)
	addWorkspaceTools(s, svc.Workspaces)
	addWorkspaceActions(s, svc.Workspaces, u)
	addWorkspaceLifecycle(s, svc.Workspaces, u)
	addProjectTools(s, svc.Projects, u.Can(auth.PermApps))
	addAppTools(s, svc.Apps)
	if u.Can(auth.PermApps) { // reading apps and logs stays for everyone
		addAppActions(s, svc.Apps)
		addComposeTools(s, svc.Compose)
	}
	if u.Can(auth.PermDatabases) {
		addDatabaseTools(s, svc.Databases)
	}
	return s
}

var ro = &sdk.ToolAnnotations{ReadOnlyHint: true}

func addNodeTools(s *sdk.Server, nodes *fleet.Service) {
	sdk.AddTool(s, &sdk.Tool{Name: "list_nodes", Annotations: ro,
		Description: "List the machines joined to the hub: roles (dev runs workspaces), status (online/offline) and telemetry (CPU, memory, disk, Podman)."},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			list, err := nodes.List(ctx)
			return nil, map[string]any{"nodes": orEmpty(list)}, err
		})
}

// orEmpty makes a nil list encode as [] rather than null.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
