package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// An app deploys by itself in one of two ways, besides the button:
//   - on_push: the forge sends a signed webhook for every push; a push to the app's branch deploys it.
//   - on_ci: the forge's CI runs the tests and, if they pass, calls /hooks/deploy with a deploy token.
//
// Walroos isn't a CI engine: the tests run in the forge. internal/hooks serves those calls.
const (
	Manual = "manual"
	OnPush = "on_push"
	OnCI   = "on_ci"
)

var ErrTrigger = whttp.Invalid("trigger must be manual, on_push or on_ci")

// SetTrigger chooses how the app deploys. on_push makes the webhook secret the first time.
func (s *Service) SetTrigger(ctx context.Context, name, trigger string) (App, error) {
	if trigger != Manual && trigger != OnPush && trigger != OnCI {
		return App{}, ErrTrigger
	}
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	if trigger == OnPush {
		if _, err := s.WebhookSecret(ctx, name); err != nil {
			return App{}, err
		}
	}
	if err := s.store.setTrigger(ctx, a.ID, trigger); err != nil {
		return App{}, err
	}
	return s.Get(ctx, name)
}

// WebhookSecret is the secret the forge signs pushes with, made the first time it's asked for.
// Unlike the deploy token it can be shown again: it's pasted into the forge's webhook form.
func (s *Service) WebhookSecret(ctx context.Context, name string) (string, error) {
	t, err := s.HookTarget(ctx, name)
	if err != nil || t.Secret != "" {
		return t.Secret, err
	}
	return s.RotateWebhookSecret(ctx, name)
}

// RotateWebhookSecret replaces the secret; the forge needs the new one.
func (s *Service) RotateWebhookSecret(ctx context.Context, name string) (string, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	secret := rand.Text()
	sealed, err := s.box.Seal(secret)
	if err != nil {
		return "", err
	}
	return secret, s.store.setHookSecrets(ctx, a.ID, "webhook_secret", sealed)
}

// NewDeployToken makes a token for CI to deploy this app with, replacing the old one. Only its
// hash is kept: it's shown this once.
func (s *Service) NewDeployToken(ctx context.Context, name string) (string, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	token := "wlr_deploy_" + rand.Text()
	return token, s.store.setHookSecrets(ctx, a.ID, "deploy_token_hash", agent.TokenHash(token))
}

// RevokeDeployToken makes the current token stop working.
func (s *Service) RevokeDeployToken(ctx context.Context, name string) error {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	return s.store.setHookSecrets(ctx, a.ID, "deploy_token_hash", "")
}

// HookTarget is what a hook call is checked against.
type HookTarget struct {
	App       string
	Trigger   string
	Branch    string // "" = the repo's default branch, which a push event names as such
	Image     bool   // a published image: a deploy pulls its tag again, no commit
	Secret    string // the webhook secret, opened; "" if none was made
	TokenHash string // sha256 of the deploy token; "" if none
}

func (s *Service) HookTarget(ctx context.Context, name string) (HookTarget, error) {
	t, sealed, err := s.store.hookTarget(ctx, name)
	if err != nil || sealed == "" {
		return t, err
	}
	if t.Secret, err = s.box.Open(sealed); err != nil {
		return t, fmt.Errorf("webhook secret: %w", err)
	}
	return t, nil
}

func (s *Store) setTrigger(ctx context.Context, id, trigger string) error {
	_, err := s.db.Write.ExecContext(ctx, `UPDATE apps SET trigger = ? WHERE id = ?`, trigger, id)
	return err
}

// setHookSecrets writes the webhook_secret or deploy_token_hash column.
func (s *Store) setHookSecrets(ctx context.Context, id, column, value string) error {
	if column != "webhook_secret" && column != "deploy_token_hash" {
		return fmt.Errorf("not a hook column: %s", column)
	}
	_, err := s.db.Write.ExecContext(ctx, `UPDATE apps SET `+column+` = ? WHERE id = ?`, value, id)
	return err
}

func (s *Store) hookTarget(ctx context.Context, name string) (HookTarget, string, error) {
	var t HookTarget
	var sealed, image string
	err := s.db.Read.QueryRowContext(ctx, `SELECT name, trigger, branch, image, webhook_secret, deploy_token_hash FROM apps WHERE name = ?`, name).
		Scan(&t.App, &t.Trigger, &t.Branch, &image, &sealed, &t.TokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return t, "", ErrNotFound
	}
	t.Image = image != ""
	return t, sealed, err
}
