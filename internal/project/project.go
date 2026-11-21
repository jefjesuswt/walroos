// Package project groups services like Dokploy's projects: a project holds apps and databases
// that share a podman network on their node, where they reach each other by name. The services
// themselves stay in internal/app and internal/database; a project is their folder and network.
package project

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/project/lookup"
	"github.com/jefjesuswt/walroos/shared/names"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNotFound = lookup.ErrNotFound
	ErrExists   = whttp.Conflict("a project with that name already exists")
	ErrInvalid  = whttp.Invalid("name must be a DNS label (lowercase letters, digits, '-'), description at most 500 characters")
	ErrNotEmpty = whttp.Conflict("the project still has services: delete or move them first")
	ErrDefault  = whttp.Invalid("the default project can't be deleted")
)

// Project is a folder of services. Apps and Databases are filled by Get and List.
type Project struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	CreatedAt   time.Time           `json:"created_at"`
	Apps        []app.App           `json:"apps"`
	Databases   []database.Database `json:"databases"`
}

type CreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Service struct {
	db   *config.Database
	apps *app.Service
	dbs  *database.Service
	now  func() time.Time
}

func NewService(db *config.Database, apps *app.Service, dbs *database.Service) *Service {
	return &Service{db: db, apps: apps, dbs: dbs, now: time.Now}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Project, error) {
	in.Description = strings.TrimSpace(in.Description)
	if !names.DNSLabel(in.Name) || len(in.Description) > 500 {
		return Project{}, ErrInvalid
	}
	p := Project{ID: randomID(), Name: in.Name, Description: in.Description, CreatedAt: s.now().UTC(),
		Apps: []app.App{}, Databases: []database.Database{}}
	_, err := s.db.Write.ExecContext(ctx, `INSERT INTO projects (id, name, description, created_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return Project{}, ErrExists
	}
	return p, err
}

// List returns every project with its services, asking the nodes once for all of them.
func (s *Service) List(ctx context.Context) ([]Project, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT id, name, description, created_at FROM projects
		ORDER BY CASE id WHEN 'default' THEN 1 ELSE 0 END, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Project{}
	byID := map[string]int{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Apps, p.Databases = []app.App{}, []database.Database{}
		byID[p.ID] = len(list)
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	apps, err := s.apps.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if i, ok := byID[a.ProjectID]; ok {
			list[i].Apps = append(list[i].Apps, a)
		}
	}
	dbs, err := s.dbs.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range dbs {
		if i, ok := byID[d.ProjectID]; ok {
			list[i].Databases = append(list[i].Databases, d)
		}
	}
	return list, nil
}

func (s *Service) Get(ctx context.Context, name string) (Project, error) {
	list, err := s.List(ctx) // ponytail: lists every service to show one project; fine for a homelab
	if err != nil {
		return Project{}, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// Delete removes an empty project. Its network stays on the node until podman prunes it.
// ponytail: no network cleanup; it's empty and costs nothing.
func (s *Service) Delete(ctx context.Context, name string) error {
	var id string
	err := s.db.Read.QueryRowContext(ctx, `SELECT id FROM projects WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if id == "default" {
		return ErrDefault
	}
	var n int
	s.db.Read.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM apps WHERE project_id = ?) + (SELECT count(*) FROM databases WHERE project_id = ?)`, id, id).Scan(&n)
	if n > 0 {
		return ErrNotEmpty
	}
	_, err = s.db.Write.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNotEmpty // a service landed in it between the count and the delete
	}
	return err
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
