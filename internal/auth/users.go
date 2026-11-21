package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrUserExists   = errors.New("that login is already a user")
	ErrUserNotFound = errors.New("user not found")
	ErrInvalidUser  = errors.New("login must be a Tailscale login (you@github, name@example.com) and role one of owner, deployer, developer")
	ErrLastOwner    = errors.New("the hub needs an owner: make someone else owner first")
	ErrSelf         = errors.New("you can't remove yourself or change your own role; another owner can")
	ErrOwnsThings   = errors.New("they still own workspaces: delete those first")
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
	userReply(w, http.StatusOK, list, err)
}

func (s *Store) handleAdd(w http.ResponseWriter, r *http.Request) {
	var m Member
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	m.Login = strings.TrimSpace(m.Login)
	if !loginRe.MatchString(m.Login) || !slices.Contains(Roles, m.Role) {
		userReply[any](w, 0, nil, ErrInvalidUser)
		return
	}
	m.CreatedAt = time.Now().UTC()
	userReply(w, http.StatusCreated, m, s.add(r, m))
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
		userReply[any](w, 0, nil, ErrInvalidUser)
	case login == Caller(r.Context()).Login:
		userReply[any](w, 0, nil, ErrSelf)
	default:
		userReply(w, http.StatusOK, Member{Login: login, Role: in.Role}, s.change(r, login, in.Role))
	}
}

func (s *Store) handleRemove(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	if login == Caller(r.Context()).Login {
		userReply[any](w, 0, nil, ErrSelf)
		return
	}
	if err := s.change(r, login, ""); err != nil {
		userReply[any](w, 0, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func userReply[T any](w http.ResponseWriter, code int, v T, err error) {
	switch {
	case err == nil:
		whttp.JSON(w, code, v)
	case errors.Is(err, ErrInvalidUser), errors.Is(err, ErrSelf):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrUserNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrUserExists), errors.Is(err, ErrLastOwner), errors.Is(err, ErrOwnsThings):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
