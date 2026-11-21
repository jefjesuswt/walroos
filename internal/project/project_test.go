package project_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// fakeAgent remembers the databases it was asked to create (with their network) and answers the
// list calls projects make.
type fakeAgent struct {
	mu  sync.Mutex
	dbs []agent.DatabaseSpec
}

func (f *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/apps":
		w.Write([]byte("[]"))
	case r.Method == "GET" && r.URL.Path == "/v1/databases":
		list := []agent.Database{}
		for _, d := range f.dbs {
			list = append(list, agent.Database{Name: d.Name, Engine: d.Engine, Version: d.Version, State: "active"})
		}
		json.NewEncoder(w).Encode(list)
	case r.Method == "POST" && r.URL.Path == "/v1/databases":
		var spec agent.DatabaseSpec
		json.NewDecoder(r.Body).Decode(&spec)
		if !spec.Valid() {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid"}`))
			return
		}
		f.dbs = append(f.dbs, spec)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(agent.Database{Name: spec.Name, State: "activating"})
	default:
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"unexpected"}`))
	}
}

func newHub(t *testing.T) (http.Handler, *fakeAgent) {
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
	users := auth.NewStore(db)
	users.EnsureOwner(ctx, "owner@github")
	db.Write.Exec(`INSERT INTO users (login, role, created_at) VALUES ('dev@github', 'developer', ?)`, now)
	if _, err := db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen) VALUES ('n1', 'prod-1', 'prod', 'h', '100.64.0.1', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	// An app from before projects: the migration put it in default.
	if _, err := db.Write.Exec(`INSERT INTO apps (id, name, node_id, repo_url, port, created_at) VALUES ('a0', 'legacy', 'n1', 'https://github.com/me/legacy', 80, ?)`, now); err != nil {
		t.Fatal(err)
	}

	fa := &fakeAgent{}
	srv := httptest.NewServer(http.HandlerFunc(fa.serve))
	t.Cleanup(srv.Close)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dial}, Timeout: 5 * time.Second}}
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	nodes := fleet.NewService(fleet.NewStore(db))
	apps := app.NewService(app.NewStore(db), nodes, agents, box)
	dbs := database.NewService(database.NewStore(db), nodes, agents, box, apps)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(func(req *http.Request) (string, error) { return req.Header.Get("X-Login"), nil }, users))
		app.NewHandler(apps).Routes(r)
		database.NewHandler(dbs).Routes(r)
		project.NewHandler(project.NewService(db, apps, dbs)).Routes(r)
	})
	return r, fa
}

func call(t *testing.T, h http.Handler, method, path string, body, out any) (int, string) {
	t.Helper()
	return callAs(t, h, "owner@github", method, path, body, out)
}

func callAs(t *testing.T, h http.Handler, login, method, path string, body, out any) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("X-Login", login)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw := rec.Body.Bytes()
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	var e struct{ Error string }
	json.Unmarshal(raw, &e)
	return rec.Code, e.Error
}

func TestProjects(t *testing.T) {
	h, fa := newHub(t)

	// The default project holds what existed before projects.
	var list []project.Project
	call(t, h, "GET", "/api/projects", nil, &list)
	if len(list) != 1 || list[0].Name != "default" || len(list[0].Apps) != 1 || list[0].Apps[0].Name != "legacy" {
		t.Fatalf("projects at start = %+v", list)
	}

	for _, c := range []struct {
		body map[string]string
		code int
		msg  string
	}{
		{map[string]string{"name": "shop", "description": "  the store  "}, 201, ""},
		{map[string]string{"name": "shop"}, 409, "already exists"},
		{map[string]string{"name": "default"}, 409, "already exists"},
		{map[string]string{"name": "Bad Name"}, 400, "DNS label"},
		{map[string]string{"name": "x", "description": strings.Repeat("a", 501)}, 400, "500"},
	} {
		if code, msg := call(t, h, "POST", "/api/projects", c.body, nil); code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("create %v: %d %q, want %d %q", c.body["name"], code, msg, c.code, c.msg)
		}
	}
	if code, _ := callAs(t, h, "dev@github", "POST", "/api/projects", map[string]string{"name": "mine"}, nil); code != 403 {
		t.Errorf("a developer created a project: %d", code)
	}

	// Services land in the project, on its network; the default project keeps the shared one.
	var d database.Database
	if code, msg := call(t, h, "POST", "/api/databases", map[string]string{"name": "shop-db", "engine": "postgres", "version": "17", "project": "shop"}, &d); code != 201 {
		t.Fatalf("database in shop: %d %s", code, msg)
	}
	call(t, h, "POST", "/api/databases", map[string]string{"name": "loose", "engine": "redis", "version": "8"}, nil)
	// Dokploy's optional fields: database, user, password and a description.
	var custom database.Database
	if code, msg := call(t, h, "POST", "/api/databases", map[string]string{"name": "custom", "engine": "postgres", "version": "17",
		"db": "orders", "user": "orders_rw", "password": "ThisIsALongPassword123", "description": "orders"}, &custom); code != 201 {
		t.Fatalf("custom credentials: %d %s", code, msg)
	}
	if last := fa.dbs[len(fa.dbs)-1]; last.DB != "orders" || last.User != "orders_rw" || last.Password != "ThisIsALongPassword123" || custom.Description != "orders" {
		t.Errorf("custom: agent got %+v, desc %q", last, custom.Description)
	}
	for _, bad := range []map[string]string{{"db": "1orders"}, {"user": "Orders"}, {"password": "short"}, {"password": "has spaces in it ok"}} {
		body := map[string]string{"name": "bad", "engine": "redis", "version": "8"}
		for k, v := range bad {
			body[k] = v
		}
		if code, _ := call(t, h, "POST", "/api/databases", body, nil); code != 400 {
			t.Errorf("database %v: %d, want 400", bad, code)
		}
	}
	if d.Project != "shop" || fa.dbs[0].Network != "walroos-p-shop" || fa.dbs[1].Network != agent.AppNetwork {
		t.Errorf("project %q, networks %q / %q", d.Project, fa.dbs[0].Network, fa.dbs[1].Network)
	}
	var a app.App
	if code, msg := call(t, h, "POST", "/api/apps", map[string]any{"name": "shop-api", "repo_url": "https://github.com/me/api", "port": 3000, "project": "shop"}, &a); code != 201 || a.Project != "shop" {
		t.Fatalf("app in shop: %d %s %+v", code, msg, a)
	}
	if code, msg := call(t, h, "POST", "/api/apps", map[string]any{"name": "x", "repo_url": "https://github.com/me/x", "port": 3000, "project": "nope"}, nil); code != 404 || !strings.Contains(msg, "project not found") {
		t.Errorf("app in an unknown project: %d %q", code, msg)
	}
	if code, msg := call(t, h, "POST", "/api/databases", map[string]string{"name": "y", "engine": "redis", "version": "8", "project": "nope"}, nil); code != 404 || !strings.Contains(msg, "project not found") {
		t.Errorf("database in an unknown project: %d %q", code, msg)
	}

	var p project.Project
	if code, _ := call(t, h, "GET", "/api/projects/shop", nil, &p); code != 200 || p.Description != "the store" || len(p.Apps) != 1 || len(p.Databases) != 1 || p.Databases[0].State != "active" {
		t.Errorf("shop = %+v", p)
	}
	if code, _ := call(t, h, "GET", "/api/projects/nope", nil, nil); code != 404 {
		t.Errorf("unknown project: %d", code)
	}
	if code, _ := callAs(t, h, "dev@github", "GET", "/api/projects/shop", nil, nil); code != 200 {
		t.Errorf("a developer can't see projects: %d", code)
	}

	// A database only links to apps of its own project: they share no network otherwise.
	if code, msg := call(t, h, "POST", "/api/databases/shop-db/link", map[string]string{"app": "legacy"}, nil); code != 400 || !strings.Contains(msg, "another project") {
		t.Errorf("link across projects: %d %q", code, msg)
	}
	if code, msg := call(t, h, "POST", "/api/databases/shop-db/link", map[string]string{"app": "shop-api"}, nil); code != 200 {
		t.Errorf("link within the project: %d %q", code, msg)
	}

	// Delete: default never, a project with services not yet, an empty one yes.
	call(t, h, "POST", "/api/projects", map[string]string{"name": "empty"}, nil)
	for path, want := range map[string]int{"/api/projects/default": 400, "/api/projects/shop": 409, "/api/projects/empty": 204, "/api/projects/gone": 404} {
		if code, msg := call(t, h, "DELETE", path, nil, nil); code != want {
			t.Errorf("DELETE %s: %d %q, want %d", path, code, msg, want)
		}
	}
	call(t, h, "GET", "/api/projects", nil, &list)
	if len(list) != 2 || list[0].Name != "shop" || list[1].Name != "default" {
		t.Errorf("projects at the end: %+v (default goes last)", list)
	}
}
