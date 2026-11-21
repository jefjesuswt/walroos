// Package envstore keeps environment variables sealed at rest, for workspaces, apps and
// projects alike: one table per owner, the same columns (owner, key, value, secret).
package envstore

import (
	"context"
	"fmt"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/shared/names"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// Var is one variable. Secret only means the UI masks it; every value is sealed.
type Var struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

var ErrInvalid = whttp.Invalid("variables: at most 64, names A-Z, 0-9 and _ (not starting with a digit), unique, values up to 32 KB")

// Seal checks the variables and seals their values for storage.
func Seal(box *secretbox.Box, vars []Var) ([]Var, error) {
	if len(vars) > 64 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalid, len(vars))
	}
	seen := map[string]bool{}
	sealed := make([]Var, 0, len(vars))
	for _, v := range vars {
		if !names.EnvKey(v.Key) || seen[v.Key] {
			return nil, fmt.Errorf("%w: %q is not a valid or unique variable name", ErrInvalid, v.Key)
		}
		if len(v.Value) > 32<<10 {
			return nil, fmt.Errorf("%w: %s is too long", ErrInvalid, v.Key)
		}
		seen[v.Key] = true
		enc, err := box.Seal(v.Value)
		if err != nil {
			return nil, err
		}
		sealed = append(sealed, Var{Key: v.Key, Value: enc, Secret: v.Secret})
	}
	return sealed, nil
}

// Open opens sealed values in place. One that won't open (another key, corrupt) becomes "",
// so one bad value doesn't hide the rest.
func Open(box *secretbox.Box, vars []Var) []Var {
	for i := range vars {
		v, err := box.Open(vars[i].Value)
		if err != nil {
			v = ""
		}
		vars[i].Value = v
	}
	return vars
}

// Table is where one kind of owner keeps its variables.
type Table struct {
	Name  string // workspace_env, app_env, project_env
	Owner string // its owner column: workspace_id, app_id, project_id
}

var (
	Workspaces = Table{"workspace_env", "workspace_id"}
	Apps       = Table{"app_env", "app_id"}
	Projects   = Table{"project_env", "project_id"}
)

// Read returns the owner's variables, still sealed, ordered by key.
func (t Table) Read(ctx context.Context, db *config.Database, owner string) ([]Var, error) {
	rows, err := db.Read.QueryContext(ctx, `SELECT key, value, secret FROM `+t.Name+` WHERE `+t.Owner+` = ? ORDER BY key`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Var{}
	for rows.Next() {
		var v Var
		if err := rows.Scan(&v.Key, &v.Value, &v.Secret); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

// Replace swaps all of the owner's variables in one transaction. vars must be sealed.
func (t Table) Replace(ctx context.Context, db *config.Database, owner string, vars []Var) error {
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+t.Name+` WHERE `+t.Owner+` = ?`, owner); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+t.Name+` (`+t.Owner+`, key, value, secret) VALUES (?, ?, ?, ?)`,
			owner, v.Key, v.Value, v.Secret); err != nil {
			return fmt.Errorf("insert env: %w", err)
		}
	}
	return tx.Commit()
}
