package workspace

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jefjesuswt/walroos/config"
)

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

// Insert saves a new workspace. Names are unique hub-wide, since the name is also the SSH user.
func (s *Store) Insert(ctx context.Context, w Workspace) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO workspaces (id, name, owner, node_id, repo_url, branch, devcontainer_path, image, state, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Owner, w.NodeID, w.RepoURL, w.Branch, w.DevcontainerPath, w.Image, w.State, w.Error, w.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: workspaces.name") {
			return ErrExists
		}
		return fmt.Errorf("insert workspace: %w", err)
	}
	return nil
}

// get loads one row; name != "" filters by name, id != "" by id.
func (s *Store) get(ctx context.Context, id, name string) (Workspace, error) {
	q := `SELECT w.id, w.name, w.owner, w.node_id, n.name, w.repo_url, w.branch, w.devcontainer_path, w.image, w.state, w.error, w.created_at
	      FROM workspaces w JOIN nodes n ON n.id = w.node_id WHERE `
	args := []any{}
	if id != "" {
		q += "w.id = ?"
		args = append(args, id)
	} else {
		q += "w.name = ?"
		args = append(args, name)
	}
	var w Workspace
	err := s.db.Read.QueryRowContext(ctx, q, args...).Scan(&w.ID, &w.Name, &w.Owner, &w.NodeID, &w.NodeName,
		&w.RepoURL, &w.Branch, &w.DevcontainerPath, &w.Image, &w.State, &w.Error, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

func (s *Store) Get(ctx context.Context, name string) (Workspace, error) {
	return s.get(ctx, "", name)
}

func (s *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT w.id, w.name, w.owner, w.node_id, n.name, w.repo_url, w.branch, w.devcontainer_path, w.image, w.state, w.error, w.created_at
		 FROM workspaces w JOIN nodes n ON n.id = w.node_id ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Owner, &w.NodeID, &w.NodeName, &w.RepoURL, &w.Branch,
			&w.DevcontainerPath, &w.Image, &w.State, &w.Error, &w.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	return list, rows.Err()
}

// UpdateState mirrors what the agent reports.
func (s *Store) UpdateState(ctx context.Context, id, state, image, msg string) error {
	_, err := s.db.Write.ExecContext(ctx,
		`UPDATE workspaces SET state = ?, image = ?, error = ? WHERE id = ?`, state, image, msg, id)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.Write.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows != 1 {
		return ErrNotFound
	}
	return nil
}

// nodeUsage counts workspaces per node, to pick the least loaded dev node.
func (s *Store) nodeUsage(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT node_id, COUNT(*) FROM workspaces GROUP BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	usage := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		usage[id] = n
	}
	return usage, rows.Err()
}

// randomID mirrors fleet's ids (16 hex chars).
func randomID() string {
	b := make([]byte, 8)
	rand.Read(b) // never fails (crypto/rand, Go 1.24+)
	return hex.EncodeToString(b)
}
