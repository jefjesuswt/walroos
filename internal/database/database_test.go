package database_test

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
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// fakeAgent keeps databases and backups in memory: the agent's database API, no podman.
type fakeAgent struct {
	mu       sync.Mutex
	dbs      map[string]agent.DatabaseSpec
	backups  map[string][]agent.Backup
	failNext string // the next create or backup answers 500 with it
	busy     bool   // backup, restore and delete answer 409
	keeps    []int
	restored []string
	deleted  []string // "<name>?purge=1"
}

func (f *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(code int, msg string) { reply(code, map[string]string{"error": msg}) }
	rest := strings.TrimPrefix(r.URL.Path, "/v1/databases")
	name, sub, _ := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	_, known := f.dbs[name]

	switch {
	case r.URL.Path == "/v1/apps":
		reply(200, []agent.App{})
	case r.Method == "GET" && rest == "":
		list := []agent.Database{}
		for n, s := range f.dbs {
			list = append(list, agent.Database{Name: n, Engine: s.Engine, Version: s.Version, Container: agent.DatabaseContainer(n), State: "active"})
		}
		reply(200, list)
	case r.Method == "POST" && rest == "":
		var spec agent.DatabaseSpec
		json.NewDecoder(r.Body).Decode(&spec)
		if f.failNext != "" {
			fail(500, f.failNext)
			f.failNext = ""
			return
		}
		if !spec.Valid() {
			fail(400, "invalid database")
			return
		}
		f.dbs[spec.Name] = spec
		reply(201, agent.Database{Name: spec.Name, Engine: spec.Engine, Version: spec.Version, State: "activating"})
	case !known:
		fail(404, "database not found on this node")
	case f.busy && r.Method != "GET":
		fail(409, "busy")
	case r.Method == "DELETE":
		delete(f.dbs, name)
		f.deleted = append(f.deleted, name+"?"+r.URL.RawQuery)
		w.WriteHeader(204)
	case r.Method == "POST" && sub == "backups":
		var in struct{ Keep int }
		json.NewDecoder(r.Body).Decode(&in)
		f.keeps = append(f.keeps, in.Keep)
		if f.failNext != "" {
			fail(500, f.failNext)
			f.failNext = ""
			return
		}
		b := agent.Backup{File: name + "-20261121T120000Z.sql.gz", Size: 42, CreatedAt: time.Now().UTC()}
		f.backups[name] = append([]agent.Backup{b}, f.backups[name]...)
		reply(201, b)
	case r.Method == "GET" && sub == "backups":
		reply(200, append([]agent.Backup{}, f.backups[name]...))
	case r.Method == "GET" && strings.HasPrefix(sub, "backups/"):
		file := strings.TrimPrefix(sub, "backups/")
		for _, b := range f.backups[name] {
			if b.File == file {
				w.Header().Set("Content-Type", "application/gzip")
				w.Write([]byte("dump of " + name))
				return
			}
		}
		fail(404, "backup not found")
	case r.Method == "POST" && sub == "restore":
		var in struct{ File string }
		json.NewDecoder(r.Body).Decode(&in)
		for _, b := range f.backups[name] {
			if b.File == in.File {
				f.restored = append(f.restored, in.File)
				w.WriteHeader(204)
				return
			}
		}
		fail(404, "backup not found")
	default:
		fail(500, "unexpected "+r.Method+" "+r.URL.Path)
	}
}

type hub struct {
	http  http.Handler
	agent *fakeAgent
	db    *config.Database
	svc   *database.Service
	apps  *app.Service
}

func newHub(t *testing.T) *hub {
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
	users := auth.NewStore(db)
	users.EnsureOwner(ctx, "owner@github")
	now := time.Now().UTC()
	for _, n := range []struct{ id, name, roles string }{{"n1", "prod-1", "prod"}, {"n2", "prod-2", "prod"}, {"n3", "dev-1", "dev"}} {
		if _, err := db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
			VALUES (?, ?, ?, ?, '100.64.0.1', ?, ?)`, n.id, n.name, n.roles, "h-"+n.id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	fa := &fakeAgent{dbs: map[string]agent.DatabaseSpec{}, backups: map[string][]agent.Backup{}}
	srv := httptest.NewServer(http.HandlerFunc(fa.serve))
	t.Cleanup(srv.Close)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dial}, Timeout: 5 * time.Second}}
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	nodes := fleet.NewService(fleet.NewStore(db))
	apps := app.NewService(app.NewStore(db), nodes, agents, box, settings.NewService(db, box))
	svc := database.NewService(database.NewStore(db), nodes, agents, box, apps)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		app.NewHandler(apps).Routes(r)
		database.NewHandler(svc).Routes(r)
	})
	return &hub{http: r, agent: fa, db: db, svc: svc, apps: apps}
}

func (h *hub) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	return rec
}

func (h *hub) call(t *testing.T, method, path string, body, out any) int {
	t.Helper()
	rec := h.do(t, method, path, body)
	if out != nil && rec.Code < 300 {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return rec.Code
}

func (h *hub) callErr(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	rec := h.do(t, method, path, body)
	var e struct{ Error string }
	json.NewDecoder(rec.Body).Decode(&e)
	return rec.Code, e.Error
}

func TestCreateDatabase(t *testing.T) {
	h := newHub(t)

	var engines []agent.Engine
	if h.call(t, "GET", "/api/databases/engines", nil, &engines); len(engines) < 3 || engines[0].ID != "postgres" {
		t.Fatalf("engines = %+v", engines)
	}

	var d database.Database
	if code := h.call(t, "POST", "/api/databases", map[string]string{"name": "shop-db", "engine": "postgres", "version": "17"}, &d); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if d.Host != "walroos-db-shop-db" || d.Port != 5432 || d.NodeName != "prod-1" || d.User != "shop_db" || d.URL != "" ||
		d.BackupEveryHours != 24 || d.BackupKeep != 7 {
		t.Errorf("created = %+v", d)
	}
	spec := h.agent.dbs["shop-db"]
	if spec.DB != "shop_db" || len(spec.Password) < 16 {
		t.Errorf("agent got %+v", spec)
	}

	// Get carries the URL with the password; the list doesn't; the DB stores it sealed.
	var got database.Database
	h.call(t, "GET", "/api/databases/shop-db", nil, &got)
	if want := "postgres://shop_db:" + spec.Password + "@walroos-db-shop-db:5432/shop_db"; got.URL != want || got.State != "active" {
		t.Errorf("get: url %q state %q, want %q", got.URL, got.State, want)
	}
	var list []database.Database
	h.call(t, "GET", "/api/databases", nil, &list)
	if len(list) != 1 || list[0].URL != "" || list[0].State != "active" {
		t.Errorf("list = %+v", list)
	}
	var sealed string
	h.db.Read.QueryRow(`SELECT password FROM databases`).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, spec.Password) {
		t.Errorf("password stored as %q", sealed)
	}

	// Redis: no user in the URL; a name starting with a digit gets a db_ identifier.
	h.call(t, "POST", "/api/databases", map[string]string{"name": "1cache", "engine": "redis", "version": "8", "node": "prod-2"}, &d)
	h.call(t, "GET", "/api/databases/1cache", nil, &got)
	if !strings.HasPrefix(got.URL, "redis://:") || !strings.HasSuffix(got.URL, "@walroos-db-1cache:6379/0") || got.NodeName != "prod-2" || got.User != "db_1cache" {
		t.Errorf("redis = %+v", got)
	}
}

func TestCreateDatabaseSad(t *testing.T) {
	h := newHub(t)
	h.call(t, "POST", "/api/databases", map[string]string{"name": "taken", "engine": "postgres", "version": "17"}, nil)
	for _, c := range []struct {
		body map[string]string
		code int
		msg  string
	}{
		{map[string]string{"name": "Bad Name", "engine": "postgres", "version": "17"}, 400, "DNS label"},
		{map[string]string{"name": "engines", "engine": "postgres", "version": "17"}, 400, "DNS label"},
		{map[string]string{"name": "a", "engine": "oracle", "version": "23"}, 400, "engine and version"},
		{map[string]string{"name": "a", "engine": "postgres", "version": "9"}, 400, "engine and version"},
		{map[string]string{"name": "taken", "engine": "redis", "version": "8"}, 409, "already exists"},
		{map[string]string{"name": "a", "engine": "redis", "version": "8", "node": "dev-1"}, 409, "prod role"},
		{map[string]string{"name": "a", "engine": "redis", "version": "8", "node": "nope"}, 404, "not found"},
	} {
		if code, msg := h.callErr(t, "POST", "/api/databases", c.body); code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("%v: %d %q, want %d %q", c.body, code, msg, c.code, c.msg)
		}
	}
	// The node refuses: no row is left behind.
	h.agent.failNext = "podman: out of disk"
	if code, msg := h.callErr(t, "POST", "/api/databases", map[string]string{"name": "broken", "engine": "redis", "version": "8"}); code != 502 || !strings.Contains(msg, "out of disk") {
		t.Errorf("node failure: %d %q", code, msg)
	}
	if code := h.call(t, "GET", "/api/databases/broken", nil, nil); code != 404 {
		t.Errorf("row after a failed create: %d", code)
	}
	// No online prod node.
	h.db.Write.Exec(`UPDATE nodes SET last_seen = ?`, time.Now().Add(-time.Hour))
	if code, msg := h.callErr(t, "POST", "/api/databases", map[string]string{"name": "late", "engine": "redis", "version": "8"}); code != 409 || !strings.Contains(msg, "no online node") {
		t.Errorf("no node: %d %q", code, msg)
	}
	// A node that went quiet shows its databases as node_down, without waiting on it.
	var list []database.Database
	h.call(t, "GET", "/api/databases", nil, &list)
	if len(list) != 1 || !list[0].NodeDown {
		t.Errorf("list with the node down = %+v", list)
	}
}

func TestBackups(t *testing.T) {
	h := newHub(t)
	h.call(t, "POST", "/api/databases", map[string]string{"name": "shop", "engine": "postgres", "version": "17"}, nil)

	for _, bad := range []map[string]int{{"backup_every_hours": -1, "backup_keep": 3}, {"backup_every_hours": 6, "backup_keep": 0}, {"backup_every_hours": 721, "backup_keep": 3}} {
		if code, _ := h.callErr(t, "PUT", "/api/databases/shop/schedule", bad); code != 400 {
			t.Errorf("schedule %v: %d", bad, code)
		}
	}
	var d database.Database
	if h.call(t, "PUT", "/api/databases/shop/schedule", map[string]int{"backup_every_hours": 6, "backup_keep": 3}, &d); d.BackupEveryHours != 6 || d.BackupKeep != 3 {
		t.Errorf("schedule = %+v", d)
	}

	var b agent.Backup
	if code := h.call(t, "POST", "/api/databases/shop/backups", nil, &b); code != 201 || b.File == "" {
		t.Fatalf("backup: %d %+v", code, b)
	}
	if h.agent.keeps[0] != 3 {
		t.Errorf("keep sent = %d, want 3", h.agent.keeps[0])
	}
	h.call(t, "GET", "/api/databases/shop", nil, &d)
	if d.LastBackupAt == nil || d.LastBackupError != "" {
		t.Errorf("after backup: %+v", d)
	}

	// A failed backup is recorded, so the UI shows it.
	h.agent.failNext = "pg_dump: connection refused"
	if code, msg := h.callErr(t, "POST", "/api/databases/shop/backups", nil); code != 502 || !strings.Contains(msg, "connection refused") {
		t.Errorf("failed backup: %d %q", code, msg)
	}
	h.call(t, "GET", "/api/databases/shop", nil, &d)
	if !strings.Contains(d.LastBackupError, "connection refused") {
		t.Errorf("last_backup_error = %q", d.LastBackupError)
	}

	var list []agent.Backup
	if h.call(t, "GET", "/api/databases/shop/backups", nil, &list); len(list) != 1 {
		t.Fatalf("backups = %+v", list)
	}
	rec := h.do(t, "GET", "/api/databases/shop/backups/"+b.File, nil)
	if rec.Code != 200 || rec.Body.String() != "dump of shop" || !strings.Contains(rec.Header().Get("Content-Disposition"), b.File) {
		t.Errorf("download: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	for path, want := range map[string]int{
		"/api/databases/shop/backups/shop-20200101T000000Z.sql.gz": 404, // well formed, not on the node
		"/api/databases/shop/backups/..%2F..%2Fetc%2Fpasswd":       400,
		"/api/databases/nope/backups/" + b.File:                    404,
	} {
		if code := h.do(t, "GET", path, nil).Code; code != want {
			t.Errorf("GET %s: %d, want %d", path, code, want)
		}
	}

	if code := h.call(t, "POST", "/api/databases/shop/restore", map[string]string{"file": b.File}, nil); code != 204 || h.agent.restored[0] != b.File {
		t.Errorf("restore: %d %v", code, h.agent.restored)
	}
	for file, want := range map[string]int{"../x": 400, "": 400, "shop-20200101T000000Z.sql.gz": 404} {
		if code, msg := h.callErr(t, "POST", "/api/databases/shop/restore", map[string]string{"file": file}); code != want {
			t.Errorf("restore %q: %d %q, want %d", file, code, msg, want)
		}
	}
	h.agent.busy = true
	if code, msg := h.callErr(t, "POST", "/api/databases/shop/restore", map[string]string{"file": b.File}); code != 409 || !strings.Contains(msg, "running") {
		t.Errorf("restore while busy: %d %q", code, msg)
	}
}

// RunDue backs up what's due: never backed up and older than its interval, or last backed up
// longer ago than it. A failed attempt counts, so a broken database isn't retried every minute.
func TestScheduledBackups(t *testing.T) {
	h := newHub(t)
	for _, n := range []string{"due", "fresh", "off", "new"} {
		h.call(t, "POST", "/api/databases", map[string]string{"name": n, "engine": "redis", "version": "8"}, nil)
	}
	old := time.Now().UTC().Add(-25 * time.Hour)
	h.db.Write.Exec(`UPDATE databases SET last_backup_at = ? WHERE name = 'due'`, old)
	h.db.Write.Exec(`UPDATE databases SET last_backup_at = ? WHERE name = 'fresh'`, time.Now().UTC().Add(-time.Hour))
	h.db.Write.Exec(`UPDATE databases SET backup_every_hours = 0, created_at = ? WHERE name = 'off'`, old)

	h.svc.RunDue(context.Background())
	if len(h.agent.keeps) != 1 || len(h.agent.backups["due"]) != 1 {
		t.Fatalf("after first run: keeps %v, backups %v", h.agent.keeps, h.agent.backups)
	}
	h.svc.RunDue(context.Background())
	if len(h.agent.keeps) != 1 {
		t.Errorf("a second run backed up again: %v", h.agent.keeps)
	}

	// A database created more than an interval ago with no backup is due; a failure is recorded.
	h.db.Write.Exec(`UPDATE databases SET created_at = ? WHERE name = 'new'`, old)
	h.agent.failNext = "boom"
	h.svc.RunDue(context.Background())
	var d database.Database
	h.call(t, "GET", "/api/databases/new", nil, &d)
	if d.LastBackupAt == nil || d.LastBackupError == "" {
		t.Errorf("failed scheduled backup not recorded: %+v", d)
	}
	h.svc.RunDue(context.Background())
	if len(h.agent.backups["new"]) != 0 || len(h.agent.keeps) != 2 {
		t.Errorf("failed backup retried right away: keeps %v", h.agent.keeps)
	}
}

func TestLinkAndDelete(t *testing.T) {
	h := newHub(t)
	h.call(t, "POST", "/api/databases", map[string]string{"name": "shop-db", "engine": "postgres", "version": "17"}, nil)
	h.call(t, "POST", "/api/databases", map[string]string{"name": "cache", "engine": "redis", "version": "8"}, nil)
	h.call(t, "POST", "/api/databases", map[string]string{"name": "far", "engine": "redis", "version": "8", "node": "prod-2"}, nil)
	if code := h.call(t, "POST", "/api/apps", map[string]any{"name": "shop", "repo_url": "https://github.com/me/shop", "port": 3000, "node": "prod-1"}, nil); code != 201 {
		t.Fatalf("create app: %d", code)
	}
	h.call(t, "PUT", "/api/apps/shop/env", []workspace.EnvVar{{Key: "KEEP", Value: "1"}, {Key: "DATABASE_URL", Value: "old"}}, nil)

	var vars []workspace.EnvVar
	if code := h.call(t, "POST", "/api/databases/shop-db/link", map[string]string{"app": "shop"}, &vars); code != 200 {
		t.Fatalf("link: %d", code)
	}
	var d database.Database
	h.call(t, "GET", "/api/databases/shop-db", nil, &d)
	byKey := map[string]workspace.EnvVar{}
	for _, v := range vars {
		byKey[v.Key] = v
	}
	if byKey["DATABASE_URL"].Value != d.URL || !byKey["DATABASE_URL"].Secret || byKey["KEEP"].Value != "1" || len(vars) != 2 {
		t.Errorf("env after link = %+v", vars)
	}
	h.call(t, "POST", "/api/databases/cache/link", map[string]string{"app": "shop"}, &vars)
	if len(vars) != 3 || !strings.HasPrefix(vars[len(vars)-1].Key, "REDIS_URL") {
		t.Errorf("redis link = %+v", vars)
	}
	for _, c := range []struct {
		db   string
		body map[string]string
		code int
		msg  string
	}{
		{"far", map[string]string{"app": "shop"}, 400, "another node"},
		{"shop-db", map[string]string{"app": "nope"}, 404, "app not found"},
		{"shop-db", map[string]string{"app": "shop", "key": "bad-key"}, 400, "env variable"},
		{"nope", map[string]string{"app": "shop"}, 404, "database not found"},
	} {
		if code, msg := h.callErr(t, "POST", "/api/databases/"+c.db+"/link", c.body); code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("link %s %v: %d %q, want %d %q", c.db, c.body, code, msg, c.code, c.msg)
		}
	}

	// Delete: purge reaches the node; a database the node no longer has still goes from the hub.
	if code := h.call(t, "DELETE", "/api/databases/shop-db?purge=1", nil, nil); code != 204 || h.agent.deleted[0] != "shop-db?purge=1" {
		t.Errorf("delete: %d %v", code, h.agent.deleted)
	}
	delete(h.agent.dbs, "cache")
	if code := h.call(t, "DELETE", "/api/databases/cache", nil, nil); code != 204 {
		t.Errorf("delete one the node lost: %d", code)
	}
	h.agent.busy = true
	if code, _ := h.callErr(t, "DELETE", "/api/databases/far", nil); code != 409 {
		t.Errorf("delete while busy: %d", code)
	}
	var list []database.Database
	if h.call(t, "GET", "/api/databases", nil, &list); len(list) != 1 || list[0].Name != "far" {
		t.Errorf("left = %+v", list)
	}
}
