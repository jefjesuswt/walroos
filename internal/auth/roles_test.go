package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/sshkeys"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

const owner, deployer, developer = "owner@github", "dep@github", "dev@github"

// newHub mounts every user route like `walroos hub` does. The caller is the X-Login header.
// Agents are unreachable: a request the role allows gets past auth and fails later (502),
// which is all these tests look at.
func newHub(t *testing.T) (http.Handler, *config.Database) {
	t.Helper()
	ctx := context.Background()
	db, err := config.NewSQLite(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for login, role := range map[string]string{owner: "owner", deployer: "deployer", developer: "developer"} {
		mustExec(t, db, `INSERT INTO users (login, role, created_at) VALUES (?, ?, ?)`, login, role, now)
	}
	mustExec(t, db, `INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen) VALUES ('n1', 'box', 'dev,prod', 'h', '', ?, ?)`, now, now)
	for name, who := range map[string]string{"mine": developer, "theirs": owner} {
		mustExec(t, db, `INSERT INTO workspaces (id, name, owner, node_id, repo_url, state, created_at) VALUES (?, ?, ?, 'n1', 'https://github.com/me/x', 'running', ?)`, name, name, who, now)
	}
	mustExec(t, db, `INSERT INTO apps (id, name, node_id, repo_url, port, created_at) VALUES ('a1', 'shop', 'n1', 'https://github.com/me/shop', 3000, ?)`, now)
	mustExec(t, db, `INSERT INTO databases (id, name, node_id, engine, version, db_user, db_name, password, created_at) VALUES ('d1', 'pg', 'n1', 'postgres', '17', 'pg', 'pg', 'x', ?)`, now)

	refuse := func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("no agents in this test")
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: refuse}, Timeout: time.Second}}
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	nodes := fleet.NewService(fleet.NewStore(db))
	apps := app.NewService(app.NewStore(db), nodes, agents, box)
	users := auth.NewStore(db)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(func(req *http.Request) (string, error) { return req.Header.Get("X-Login"), nil }, users))
		r.Get("/api/me", auth.HandleMe)
		fleet.NewHandler(nodes, "http://hub", "hub", agents).UserRoutes(r)
		sshkeys.NewHandler(sshkeys.NewStore(db), nodes.Authenticate).UserRoutes(r)
		users.UserRoutes(r)
		workspace.NewHandler(workspace.NewService(workspace.NewStore(db), nodes, agents, box)).Routes(r)
		app.NewHandler(apps).Routes(r)
		database.NewHandler(database.NewService(database.NewStore(db), nodes, agents, box, apps)).Routes(r)
	})
	return r, db
}

func mustExec(t *testing.T, db *config.Database, q string, args ...any) {
	t.Helper()
	if _, err := db.Write.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func call(t *testing.T, h http.Handler, login, method, path string, body any) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("X-Login", login)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var e struct{ Error string }
	json.Unmarshal(rec.Body.Bytes(), &e)
	return rec.Code, e.Error
}

// TestRoles walks every guarded route as each role: allowed ones get past auth (any status but
// 401/403), refused ones answer 403 with a message that names the role.
func TestRoles(t *testing.T) {
	h, _ := newHub(t)
	D, V := true, true // allowed for deployer, developer (the owner is always allowed)
	for _, c := range []struct {
		method, path string
		body         any
		deployer     bool
		developer    bool
	}{
		// Everyone
		{"GET", "/api/me", nil, D, V},
		{"GET", "/api/nodes", nil, D, V},
		{"GET", "/api/workspaces", nil, D, V},
		{"GET", "/api/apps", nil, D, V},
		{"GET", "/api/apps/shop", nil, D, V},
		{"GET", "/api/apps/shop/deployments", nil, D, V},
		{"GET", "/api/apps/shop/logs", nil, D, V},
		{"GET", "/api/deployments", nil, D, V},
		{"GET", "/api/ssh-keys", nil, D, V},
		{"GET", "/api/settings/github", nil, D, V},
		{"GET", "/api/workspaces/stacks", nil, D, V},
		{"POST", "/api/workspaces/from-scratch", map[string]any{"name": "p", "stack": "go", "version": "1.26"}, D, V},
		// Apps: deployers and the owner
		{"POST", "/api/apps", map[string]any{"name": "x", "repo_url": "https://github.com/me/x", "port": 80}, D, !V},
		{"GET", "/api/apps/shop/env", nil, D, !V},
		{"PUT", "/api/apps/shop/env", []any{}, D, !V},
		{"POST", "/api/apps/shop/deploy", map[string]any{}, D, !V},
		{"PUT", "/api/apps/shop/domains", []string{}, D, !V},
		{"PUT", "/api/apps/shop/volumes", []any{}, D, !V},
		{"DELETE", "/api/apps/shop", nil, D, !V},
		{"GET", "/api/settings/cloudflare", nil, D, !V},
		// Databases: deployers and the owner
		{"GET", "/api/databases", nil, D, !V},
		{"GET", "/api/databases/pg", nil, D, !V},
		{"POST", "/api/databases", map[string]any{"name": "x", "engine": "redis", "version": "8"}, D, !V},
		{"POST", "/api/databases/pg/backups", nil, D, !V},
		{"POST", "/api/databases/pg/restore", map[string]string{"file": "pg-20261121T000000Z.sql.gz"}, D, !V},
		{"DELETE", "/api/databases/pg", nil, D, !V},
		// Owner only
		{"POST", "/api/nodes/join-tokens", nil, !D, !V},
		{"PUT", "/api/nodes/n1/roles", map[string]any{"roles": []string{"dev"}}, !D, !V},
		{"DELETE", "/api/nodes/n1", nil, !D, !V}, // FK: refused anyway, but by the role first
		{"PUT", "/api/settings/cloudflare", map[string]string{"token": "x"}, !D, !V},
		{"PUT", "/api/settings/github", map[string]string{"token": "x"}, !D, !V},
		{"DELETE", "/api/settings/github", nil, !D, !V},
		{"POST", "/api/workspaces/from-scratch", map[string]any{"name": "p", "stack": "go", "version": "1.26", "github": true}, !D, !V},
		{"GET", "/api/users", nil, !D, !V},
		// Someone else's workspace: owner only; your own: you
		{"POST", "/api/workspaces/theirs/stop", nil, !D, !V},
		{"GET", "/api/workspaces/theirs/env", nil, !D, !V},
		{"DELETE", "/api/workspaces/theirs", nil, !D, !V},
		{"POST", "/api/workspaces/mine/stop", nil, !D, V},
		{"GET", "/api/workspaces/mine/env", nil, !D, V},
	} {
		for login, allowed := range map[string]bool{owner: true, deployer: c.deployer, developer: c.developer} {
			code, msg := call(t, h, login, c.method, c.path, c.body)
			switch {
			case code == 401:
				t.Errorf("%s %s as %s: 401", c.method, c.path, login)
			case allowed && code == 403:
				t.Errorf("%s %s as %s: 403 %q, want allowed", c.method, c.path, login, msg)
			case !allowed && code != 403:
				t.Errorf("%s %s as %s: %d, want 403", c.method, c.path, login, code)
			case !allowed && !strings.Contains(msg, "role") && !strings.Contains(msg, "workspace is"):
				t.Errorf("%s %s as %s: 403 message %q doesn't say why", c.method, c.path, login, msg)
			}
		}
	}
	if code, _ := call(t, h, "stranger@github", "GET", "/api/me", nil); code != 403 {
		t.Errorf("a login not on the list: %d, want 403", code)
	}
}

func TestUsers(t *testing.T) {
	h, db := newHub(t)
	var me auth.User

	for _, c := range []struct {
		method, path string
		body         any
		code         int
		msg          string
	}{
		{"POST", "/api/users", map[string]string{"login": "new@github", "role": "deployer"}, 201, ""},
		{"POST", "/api/users", map[string]string{"login": "new@github", "role": "deployer"}, 409, "already"},
		{"POST", "/api/users", map[string]string{"login": "no-at-sign", "role": "deployer"}, 400, "Tailscale login"},
		{"POST", "/api/users", map[string]string{"login": "x@github", "role": "admin"}, 400, "role one of"},
		{"POST", "/api/users", map[string]string{"login": "x@github", "role": "member"}, 400, "role one of"},
		{"PUT", "/api/users/new@github", map[string]string{"role": "developer"}, 200, ""},
		{"PUT", "/api/users/new@github", map[string]string{"role": "root"}, 400, "role one of"},
		{"PUT", "/api/users/nobody@github", map[string]string{"role": "developer"}, 404, "not found"},
		{"PUT", "/api/users/" + owner, map[string]string{"role": "developer"}, 400, "yourself"},
		{"DELETE", "/api/users/" + owner, nil, 400, "yourself"},
		{"DELETE", "/api/users/" + developer, nil, 409, "workspaces"}, // owns "mine"
		{"DELETE", "/api/users/new@github", nil, 204, ""},
		{"DELETE", "/api/users/new@github", nil, 404, "not found"},
	} {
		code, msg := call(t, h, owner, c.method, c.path, c.body)
		if code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("%s %s %v: %d %q, want %d %q", c.method, c.path, c.body, code, msg, c.code, c.msg)
		}
	}

	// A second owner can't demote the last other owner below one: two owners, then each tries.
	call(t, h, owner, "PUT", "/api/users/"+deployer, map[string]string{"role": "owner"})
	if code, _ := call(t, h, deployer, "PUT", "/api/users/"+owner, map[string]string{"role": "developer"}); code != 200 {
		t.Fatalf("demote one of two owners: %d", code)
	}
	if code, msg := call(t, h, owner, "PUT", "/api/users/"+deployer, map[string]string{"role": "developer"}); code != 403 || !strings.Contains(msg, "role") {
		t.Errorf("a demoted owner still manages users: %d %q", code, msg)
	}
	// The one owner left can't demote themself (self) and nobody else can: the hub keeps an owner.
	var owners int
	db.Read.QueryRow(`SELECT count(*) FROM users WHERE role = 'owner'`).Scan(&owners)
	if owners != 1 {
		t.Errorf("owners = %d, want 1", owners)
	}
	// The users list shows roles; the role reaches /api/me.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("X-Login", owner)
	h.ServeHTTP(rec, req)
	json.NewDecoder(rec.Body).Decode(&me)
	if me.Role != "developer" {
		t.Errorf("/api/me role = %q, want developer", me.Role)
	}
}
