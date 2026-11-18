package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/workspace"
)

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

func (s *Store) Insert(ctx context.Context, a App) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO apps (id, name, node_id, repo_url, branch, dockerfile, port, health_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.NodeID, a.RepoURL, a.Branch, a.Dockerfile, a.Port, a.HealthPath, a.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: apps.name") {
		return ErrExists
	}
	return err
}

const appCols = `a.id, a.name, a.node_id, n.name, a.repo_url, a.branch, a.dockerfile, a.port, a.health_path, a.created_at
	FROM apps a JOIN nodes n ON n.id = a.node_id`

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var a App
	err := row.Scan(&a.ID, &a.Name, &a.NodeID, &a.NodeName, &a.RepoURL, &a.Branch, &a.Dockerfile, &a.Port, &a.HealthPath, &a.CreatedAt)
	return a, err
}

func (s *Store) Get(ctx context.Context, name string) (App, error) {
	a, err := scanApp(s.db.Read.QueryRowContext(ctx, `SELECT `+appCols+` WHERE a.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) getByID(ctx context.Context, id string) (App, error) {
	a, err := scanApp(s.db.Read.QueryRowContext(ctx, `SELECT `+appCols+` WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) List(ctx context.Context) ([]App, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT `+appCols+` ORDER BY a.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.Write.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	return err
}

const deployCols = `id, app_id, sha, image, state, error, created_at, finished_at`

func scanDeploy(row interface{ Scan(...any) error }, extra ...any) (Deployment, error) {
	var d Deployment
	err := row.Scan(append([]any{&d.ID, &d.AppID, &d.Sha, &d.Image, &d.State, &d.Error, &d.CreatedAt, &d.FinishedAt}, extra...)...)
	return d, err
}

func (s *Store) InsertDeployment(ctx context.Context, d Deployment) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO deployments (id, app_id, sha, image, state, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.AppID, d.Sha, d.Image, d.State, d.CreatedAt)
	return err
}

// Deployments lists an app's deployments, newest first, without their logs.
func (s *Store) Deployments(ctx context.Context, appID string, limit int) ([]Deployment, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT `+deployCols+` FROM deployments WHERE app_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Deployment{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// Deployment returns one deployment with its log.
func (s *Store) Deployment(ctx context.Context, appID, id string) (Deployment, error) {
	var log string
	d, err := scanDeploy(s.db.Read.QueryRowContext(ctx,
		`SELECT `+deployCols+`, log FROM deployments WHERE app_id = ? AND id = ?`, appID, id), &log)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrDeployNotFound
	}
	d.Log = log
	return d, err
}

// nextQueued is the oldest queued deployment, if any.
func (s *Store) nextQueued(ctx context.Context) (Deployment, bool, error) {
	d, err := scanDeploy(s.db.Read.QueryRowContext(ctx,
		`SELECT `+deployCols+` FROM deployments WHERE state = 'queued' ORDER BY created_at, rowid LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (s *Store) setRunning(ctx context.Context, id string) error {
	_, err := s.db.Write.ExecContext(ctx, `UPDATE deployments SET state = 'running' WHERE id = ?`, id)
	return err
}

// ponytail: the log keeps its last 1 MB; a build that prints more loses its start.
func (s *Store) appendLog(ctx context.Context, id, line string) error {
	_, err := s.db.Write.ExecContext(ctx,
		`UPDATE deployments SET log = substr(log || ? || char(10), -1000000) WHERE id = ?`, line, id)
	return err
}

func (s *Store) finish(ctx context.Context, id, state, sha, image, msg string, at time.Time) error {
	_, err := s.db.Write.ExecContext(ctx,
		`UPDATE deployments SET state = ?, sha = CASE WHEN ? = '' THEN sha ELSE ? END, image = CASE WHEN ? = '' THEN image ELSE ? END,
		 error = ?, finished_at = ? WHERE id = ?`, state, sha, sha, image, image, msg, at, id)
	return err
}

// failRunning marks deployments a previous hub process left running: their stream is gone.
// ponytail: the node may still finish one (a killed tsnet hub sends no FIN, so the agent
// doesn't notice); the node's running release is the truth. Ask the agent here if it matters.
func (s *Store) failRunning(ctx context.Context, at time.Time) error {
	_, err := s.db.Write.ExecContext(ctx,
		`UPDATE deployments SET state = 'failed', error = 'the hub restarted during the deploy; the node may have finished it, check the app''s logs', finished_at = ? WHERE state = 'running'`, at)
	return err
}

func (s *Store) Env(ctx context.Context, appID string) ([]workspace.EnvVar, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT key, value, secret FROM app_env WHERE app_id = ? ORDER BY key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []workspace.EnvVar{}
	for rows.Next() {
		var v workspace.EnvVar
		if err := rows.Scan(&v.Key, &v.Value, &v.Secret); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

func (s *Store) ReplaceEnv(ctx context.Context, appID string, vars []workspace.EnvVar) error {
	tx, err := s.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_env WHERE app_id = ?`, appID); err != nil {
		return err
	}
	for _, v := range vars {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_env (app_id, key, value, secret) VALUES (?, ?, ?, ?)`,
			appID, v.Key, v.Value, v.Secret); err != nil {
			return fmt.Errorf("insert env: %w", err)
		}
	}
	return tx.Commit()
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
