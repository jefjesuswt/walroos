package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/cloudflare"
)

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

func (s *Store) Insert(ctx context.Context, a App) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO apps (id, name, node_id, project_id, description, repo_url, image, branch, dockerfile, port, health_path, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.NodeID, cmp.Or(a.ProjectID, "default"), a.Description, a.RepoURL, a.Image, a.Branch, a.Dockerfile, a.Port, a.HealthPath, a.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: apps.name") {
		return ErrExists
	}
	return err
}

const appCols = `a.id, a.name, a.node_id, n.name, a.project_id, p.name, a.description, a.repo_url, a.image, a.volumes, a.branch, a.dockerfile, a.port, a.health_path, a.trigger, a.deploy_token_hash != '', a.created_at
	FROM apps a JOIN nodes n ON n.id = a.node_id JOIN projects p ON p.id = a.project_id`

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var a App
	var vols string
	err := row.Scan(&a.ID, &a.Name, &a.NodeID, &a.NodeName, &a.ProjectID, &a.Project, &a.Description, &a.RepoURL, &a.Image, &vols, &a.Branch, &a.Dockerfile, &a.Port, &a.HealthPath, &a.Trigger, &a.DeployToken, &a.CreatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(vols), &a.Volumes)
	}
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

func (s *Store) setSource(ctx context.Context, id string, a App) error {
	_, err := s.db.Write.ExecContext(ctx, `UPDATE apps SET repo_url = ?, image = ?, branch = ?, dockerfile = ?, port = ?, health_path = ? WHERE id = ?`,
		a.RepoURL, a.Image, a.Branch, a.Dockerfile, a.Port, a.HealthPath, id)
	return err
}

func (s *Store) setVolumes(ctx context.Context, id string, vols []agent.Volume) error {
	b, err := json.Marshal(vols)
	if err == nil {
		_, err = s.db.Write.ExecContext(ctx, `UPDATE apps SET volumes = ? WHERE id = ?`, string(b), id)
	}
	return err
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

// AllDeployments lists the newest deployments across apps, with their app's name.
func (s *Store) AllDeployments(ctx context.Context, limit int) ([]Deployment, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT d.id, d.app_id, d.sha, d.image, d.state, d.error, d.created_at, d.finished_at, a.name
		 FROM deployments d JOIN apps a ON a.id = d.app_id ORDER BY d.created_at DESC, d.rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Deployment{}
	for rows.Next() {
		var name string
		d, err := scanDeploy(rows, &name)
		if err != nil {
			return nil, err
		}
		d.AppName = name
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

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// domains maps app id to its domains, sorted.
func (s *Store) domains(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT app_id, domain FROM app_domains ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var app, d string
		if err := rows.Scan(&app, &d); err != nil {
			return nil, err
		}
		out[app] = append(out[app], d)
	}
	return out, rows.Err()
}

func (s *Store) replaceDomains(ctx context.Context, appID string, domains []string) error {
	tx, err := s.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_domains WHERE app_id = ?`, appID); err != nil {
		return err
	}
	for _, d := range domains {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_domains (domain, app_id) VALUES (?, ?)`, d, appID); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return fmt.Errorf("%w: %s", ErrDomainTaken, d)
			}
			return err
		}
	}
	return tx.Commit()
}

// nodeRules is the ingress of a node's tunnel: each domain to its app by network alias.
func (s *Store) nodeRules(ctx context.Context, nodeID string) ([]cloudflare.Rule, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT d.domain, a.name, a.port FROM app_domains d
		JOIN apps a ON a.id = d.app_id WHERE a.node_id = ? ORDER BY d.domain`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []cloudflare.Rule{}
	for rows.Next() {
		var d, name string
		var port int
		if err := rows.Scan(&d, &name, &port); err != nil {
			return nil, err
		}
		rules = append(rules, cloudflare.Rule{Hostname: d, Service: fmt.Sprintf("http://%s:%d", name, port)})
	}
	return rules, rows.Err()
}

type tunnel struct{ AccountID, ID string }

func (s *Store) tunnel(ctx context.Context, nodeID string) (tunnel, bool, error) {
	var t tunnel
	err := s.db.Read.QueryRowContext(ctx, `SELECT account_id, tunnel_id FROM tunnels WHERE node_id = ?`, nodeID).Scan(&t.AccountID, &t.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (s *Store) saveTunnel(ctx context.Context, nodeID string, t tunnel) error {
	_, err := s.db.Write.ExecContext(ctx, `INSERT INTO tunnels (node_id, account_id, tunnel_id) VALUES (?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET account_id = excluded.account_id, tunnel_id = excluded.tunnel_id`, nodeID, t.AccountID, t.ID)
	return err
}
