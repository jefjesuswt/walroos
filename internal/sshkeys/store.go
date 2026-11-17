// Package sshkeys stores users' SSH public keys and answers the node agents' question
// "may this key open this workspace?" for their SSH gateways.
package sshkeys

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
)

var (
	ErrInvalidKey  = errors.New("public_key must be one OpenSSH public key line (ssh-ed25519 AAAA... comment)")
	ErrInvalidName = errors.New("name must be 1-64 characters")
	ErrKeyExists   = errors.New("this key, or a key with this name, is already added")
	ErrKeyNotFound = errors.New("ssh key not found")
	ErrDenied      = errors.New("key not allowed on this workspace")
)

type Key struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

func (s *Store) List(ctx context.Context, login string) ([]Key, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, name, public_key, fingerprint, created_at FROM ssh_keys WHERE login = ? ORDER BY name`, login)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) Add(ctx context.Context, login string, k Key) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO ssh_keys (id, login, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		k.ID, login, k.Name, k.PublicKey, k.Fingerprint, k.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrKeyExists
	}
	return err
}

// Delete removes one of login's keys; someone else's key id is reported as not found.
func (s *Store) Delete(ctx context.Context, login, id string) error {
	res, err := s.db.Write.ExecContext(ctx, `DELETE FROM ssh_keys WHERE id = ? AND login = ?`, id, login)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// Authorize returns the login that owns the key with this fingerprint if it may open the
// workspace on the node: the hub's owner opens any workspace, a member only the ones they own.
func (s *Store) Authorize(ctx context.Context, nodeID, workspace, fingerprint string) (string, error) {
	var login, role, wsOwner string
	err := s.db.Read.QueryRowContext(ctx, `
		SELECT k.login, u.role, COALESCE(w.owner, '')
		FROM ssh_keys k
		JOIN users u ON u.login = k.login
		LEFT JOIN workspaces w ON w.name = ? AND w.node_id = ?
		WHERE k.fingerprint = ?`, workspace, nodeID, fingerprint).Scan(&login, &role, &wsOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", fmt.Errorf("authorize ssh key: %w", err)
	}
	// ponytail: workspaces created straight on an agent have no hub row yet (until the hub's
	// workspace service), so only the owner can reach those.
	if role != "owner" && wsOwner != login {
		return "", ErrDenied
	}
	return login, nil
}
