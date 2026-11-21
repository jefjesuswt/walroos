// Package settings keeps the hub's integrations: the Cloudflare and GitHub API tokens, sealed
// in the settings table. Apps ask it for a Cloudflare client (domains), workspaces for a GitHub
// one (new projects' repos). A token never leaves the hub; the UI only sees a status.
package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var ErrEmptyToken = whttp.Invalid("token is empty")

type Service struct {
	db  *config.Database
	box *secretbox.Box

	CloudflareAPI string // "" = Cloudflare's; tests point it at a fake
	GitHubAPI     string // "" = GitHub's
}

func NewService(db *config.Database, box *secretbox.Box) *Service {
	return &Service{db: db, box: box}
}

// get reads a setting ("" if unset).
func (s *Service) get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.Read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// set writes several settings at once; a "" value removes the key.
func (s *Service) set(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range kv {
		if v == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value`, k, v)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// token opens the sealed token under key; "" if none is saved.
func (s *Service) token(ctx context.Context, key string) (string, error) {
	sealed, err := s.get(ctx, key)
	if err != nil || sealed == "" {
		return "", err
	}
	t, err := s.box.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return t, nil
}

// seal returns the token sealed for storage, or ErrEmptyToken.
func (s *Service) seal(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", ErrEmptyToken
	}
	sealed, err := s.box.Seal(token)
	return token, sealed, err
}
