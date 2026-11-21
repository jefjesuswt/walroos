// Package lookup resolves a project name to its ID for the services created in it. It's apart
// from package project, which lists apps and databases, so app and database can use it.
package lookup

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var ErrNotFound = whttp.NotFound("project not found")

// Default holds what's created without a project.
const Default = "default"

// ID resolves a project name ("" = default).
func ID(ctx context.Context, db *config.Database, name string) (string, error) {
	if name == "" {
		name = Default
	}
	var id string
	err := db.Read.QueryRowContext(ctx, `SELECT id FROM projects WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
