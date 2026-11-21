package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
)

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

func (s *Store) Insert(ctx context.Context, d Database) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO databases (id, name, node_id, project_id, description, engine, version, db_user, db_name, password, backup_every_hours, backup_keep, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.NodeID, d.ProjectID, d.Description, d.Engine, d.Version, d.User, d.DB, d.password, d.BackupEveryHours, d.BackupKeep, d.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: databases.name") {
		return ErrExists
	}
	return err
}

const cols = `d.id, d.name, d.node_id, n.name, d.project_id, p.name, d.description, d.engine, d.version, d.db_user, d.db_name, d.password,
	d.backup_every_hours, d.backup_keep, d.last_backup_at, d.last_backup_error, d.created_at
	FROM databases d JOIN nodes n ON n.id = d.node_id JOIN projects p ON p.id = d.project_id`

func scan(row interface{ Scan(...any) error }) (Database, error) {
	var d Database
	var last sql.NullTime
	err := row.Scan(&d.ID, &d.Name, &d.NodeID, &d.NodeName, &d.ProjectID, &d.Project, &d.Description, &d.Engine, &d.Version, &d.User, &d.DB, &d.password,
		&d.BackupEveryHours, &d.BackupKeep, &last, &d.LastBackupError, &d.CreatedAt)
	if last.Valid {
		t := last.Time
		d.LastBackupAt = &t
	}
	return d, err
}

func (s *Store) Get(ctx context.Context, name string) (Database, error) {
	d, err := scan(s.db.Read.QueryRowContext(ctx, `SELECT `+cols+` WHERE d.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) List(ctx context.Context) ([]Database, error) {
	rows, err := s.db.Read.QueryContext(ctx, `SELECT `+cols+` ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Database{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *Store) setSchedule(ctx context.Context, id string, sc Schedule) error {
	_, err := s.db.Write.ExecContext(ctx, `UPDATE databases SET backup_every_hours = ?, backup_keep = ? WHERE id = ?`, sc.EveryHours, sc.Keep, id)
	return err
}

func (s *Store) backedUp(ctx context.Context, id string, at time.Time, msg string) error {
	_, err := s.db.Write.ExecContext(ctx, `UPDATE databases SET last_backup_at = ?, last_backup_error = ? WHERE id = ?`, at, msg, id)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.Write.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
	return err
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
