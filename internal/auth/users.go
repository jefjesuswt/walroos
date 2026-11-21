package auth

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrUserExists   = whttp.Conflict("that login is already a user")
	ErrUserNotFound = whttp.NotFound("user not found")
	ErrInvalidUser  = whttp.Invalid("login must be a Tailscale login (you@github, name@example.com) and role one of owner, deployer, developer")
	ErrLastOwner    = whttp.Conflict("the hub needs an owner: make someone else owner first")
	ErrSelf         = whttp.Invalid("you can't remove yourself or change your own role; another owner can")
	ErrOwnsThings   = whttp.Conflict("they still own workspaces: delete those first")
)

// A Tailscale login name: user@provider or an email. Kept loose; WhoIs decides who matches.
var loginRe = regexp.MustCompile(`^[^\s@]{1,128}@[^\s@]{1,253}$`)

// Member is a row of the users list.
type Member struct {
	Login     string    `json:"login"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) List(r *http.Request) ([]Member, error) {
	rows, err := s.db.Read.QueryContext(r.Context(), `SELECT login, role, created_at FROM users ORDER BY created_at, login`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Login, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *Store) add(r *http.Request, m Member) error {
	_, err := s.db.Write.ExecContext(r.Context(), `INSERT INTO users (login, role, created_at) VALUES (?, ?, ?)`, m.Login, m.Role, m.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrUserExists
	}
	return err
}

// change sets a role ("" removes the user) unless that leaves the hub without an owner. The
// owner count is read in the same transaction as the write.
func (s *Store) change(r *http.Request, login, role string) error {
	tx, err := s.db.Write.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(r.Context(), `SELECT role FROM users WHERE login = ?`, login).Scan(&current); err != nil {
		return ErrUserNotFound
	}
	if current == Owner && role != Owner {
		var owners int
		tx.QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE role = 'owner'`).Scan(&owners)
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	if role == "" {
		// Their workspaces stay (they point at the login); their SSH keys go with them.
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM users WHERE login = ?`, login); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return ErrOwnsThings
			}
			return err
		}
	} else if _, err := tx.ExecContext(r.Context(), `UPDATE users SET role = ? WHERE login = ?`, role, login); err != nil {
		return err
	}
	return tx.Commit()
}

// UserRoutes manage who can use the hub; owner only.
func (s *Store) UserRoutes(r *whttp.Router) {
	o := r.With(Require(PermUsers))
	o.Get("/api/users", s.handleList)
	o.Post("/api/users", s.handleAdd)
	o.Put("/api/users/{login}", s.handleSetRole)
	o.Delete("/api/users/{login}", s.handleRemove)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r)
	whttp.Reply(w, http.StatusOK, list, err)
}

func (s *Store) handleAdd(w http.ResponseWriter, r *http.Request) {
	var m Member
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m.Login = strings.TrimSpace(m.Login)
	if !loginRe.MatchString(m.Login) || !slices.Contains(Roles, m.Role) {
		whttp.Reply(w, 0, nil, ErrInvalidUser)
		return
	}
	m.CreatedAt = time.Now().UTC()
	whttp.Reply(w, http.StatusCreated, m, s.add(r, m))
}

func (s *Store) handleSetRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	login := r.PathValue("login")
	switch {
	case !slices.Contains(Roles, in.Role):
		whttp.Reply(w, 0, nil, ErrInvalidUser)
	case login == Caller(r.Context()).Login:
		whttp.Reply(w, 0, nil, ErrSelf)
	default:
		whttp.Reply(w, http.StatusOK, Member{Login: login, Role: in.Role}, s.change(r, login, in.Role))
	}
}

func (s *Store) handleRemove(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	if login == Caller(r.Context()).Login {
		whttp.Reply(w, 0, nil, ErrSelf)
		return
	}
	if err := s.change(r, login, ""); err != nil {
		whttp.Reply(w, 0, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
