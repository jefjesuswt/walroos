package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/jefjesuswt/walroos/internal/database"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
