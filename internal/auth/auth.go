// Package auth identifies hub users by their Tailscale login. There are no passwords:
// the tailnet already authenticated the device, the hub only checks an allowlist.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"tailscale.com/client/local"
)

type User struct {
	Login string `json:"login"`
	Role  string `json:"role"`
}

// Identify returns the login of whoever sent the request.
type Identify func(*http.Request) (string, error)

// Tailscale identifies the caller with the tailnet's WhoIs.
func Tailscale(lc *local.Client) Identify {
	return func(r *http.Request) (string, error) {
		who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			return "", err
		}
		if who.Node.IsTagged() {
			return "", errors.New("tagged devices act as nodes, not users")
		}
		return who.UserProfile.LoginName, nil
	}
}

// Static treats every request as login. Only for --dev on localhost.
func Static(login string) Identify {
	return func(*http.Request) (string, error) { return login, nil }
}

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

// EnsureOwner adds login as owner if it isn't a user yet.
func (s *Store) EnsureOwner(ctx context.Context, login string) error {
	_, err := s.db.Write.ExecContext(ctx,
		`INSERT INTO users (login, role, created_at) VALUES (?, 'owner', ?) ON CONFLICT (login) DO NOTHING`,
		login, time.Now().UTC())
	return err
}

func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.Read.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) Get(ctx context.Context, login string) (User, error) {
	u := User{Login: login}
	err := s.db.Read.QueryRowContext(ctx, `SELECT role FROM users WHERE login = ?`, login).Scan(&u.Role)
	return u, err
}

type ctxKey struct{}

// Middleware rejects callers that can't be identified (401) or aren't on the allowlist (403).
func Middleware(identify Identify, users *Store) whttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			login, err := identify(r)
			if err != nil || login == "" {
				whttp.ErrorJSON(w, http.StatusUnauthorized, "can't identify you on the tailnet")
				return
			}

			u, err := users.Get(r.Context(), login)
			if errors.Is(err, sql.ErrNoRows) {
				whttp.ErrorJSON(w, http.StatusForbidden, login+" is not allowed on this hub")
				return
			}
			if err != nil {
				whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
		})
	}
}

func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// HandleMe serves GET /api/me.
func HandleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := FromContext(r.Context())
	whttp.JSON(w, http.StatusOK, u)
}
