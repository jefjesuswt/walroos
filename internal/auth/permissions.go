package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

// Roles are fixed: the hub is for its owner and a few trusted guests.
//   - owner: everything, including nodes, settings (tokens) and users.
//   - deployer: apps and databases too, on top of their own workspaces.
//   - developer: their own workspaces; apps read-only (no env), no databases.
const (
	Owner     = "owner"
	Deployer  = "deployer"
	Developer = "developer"
)

var Roles = []string{Owner, Deployer, Developer}

// Perm is something a role may do beyond working on its own workspaces.
type Perm string

const (
	PermApps         Perm = "apps"          // create, deploy, change and delete apps; read their env
	PermDatabases    Perm = "databases"     // everything about databases, passwords included
	PermNodes        Perm = "nodes"         // add, remove and change nodes
	PermSettings     Perm = "settings"      // Cloudflare and GitHub tokens, projects on the owner's GitHub
	PermUsers        Perm = "users"         // who can use the hub
	PermAnyWorkspace Perm = "any-workspace" // act on workspaces other users own
)

var grants = map[string][]Perm{
	Owner:    {PermApps, PermDatabases, PermNodes, PermSettings, PermUsers, PermAnyWorkspace},
	Deployer: {PermApps, PermDatabases},
}

// ErrForbidden is what a handler answers with 403.
var ErrForbidden = whttp.Forbidden("your role can't do that")

func (u User) Can(p Perm) bool {
	for _, g := range grants[u.Role] {
		if g == p {
			return true
		}
	}
	return false
}

// Check returns an ErrForbidden that names the role and what it lacks, for the UI and MCP.
func (u User) Check(p Perm) error {
	if u.Can(p) {
		return nil
	}
	return fmt.Errorf("%w: %s users can't manage %s; ask the hub's owner", ErrForbidden, u.Role, p)
}

// CanUseWorkspace: everyone works on their own workspaces; the owner on anyone's.
func (u User) CanUseWorkspace(owner string) error {
	if owner == u.Login || u.Can(PermAnyWorkspace) {
		return nil
	}
	return fmt.Errorf("%w: that workspace is %s's", ErrForbidden, owner)
}

// Require answers 403 unless the caller's role has p. Mount behind Middleware.
func Require(p Perm) whttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, _ := FromContext(r.Context())
			if err := u.Check(p); err != nil {
				whttp.WriteError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Caller is the user of ctx, set by Middleware.
func Caller(ctx context.Context) User {
	u, _ := FromContext(ctx)
	return u
}
