package compose_test

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
	"github.com/jefjesuswt/walroos/internal/compose"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/shared/envref"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// node is a fake agent: it serves a repo's files, deploys whatever it's asked and lists it.
type node struct {
	mu    sync.Mutex
	repo  map[string]string
	specs map[string]agent.AppSpec
	gone  []string
}

func (n *node) serve(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/repo/files":
		var in agent.RepoFilesRequest
		json.NewDecoder(r.Body).Decode(&in)
		files := map[string]string{}
		for _, p := range in.Paths {
			if v, ok := n.repo[p]; ok {
				files[p] = v
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"files": files})
	case strings.HasSuffix(r.URL.Path, "/deploy"):
		var s agent.AppSpec
		json.NewDecoder(r.Body).Decode(&s)
		n.specs[s.Name] = s
		json.NewEncoder(w).Encode(agent.DeployLine{Release: &agent.Release{Sha: "abc123", Image: "img", Container: s.Name + "-" + s.ID}})
	case r.Method == "GET" && r.URL.Path == "/v1/apps":
		list := []agent.App{}
		for name, s := range n.specs {
			list = append(list, agent.App{Name: name, Container: name + "-" + s.ID, State: "active"})
		}
		json.NewEncoder(w).Encode(list)
	case r.Method == "DELETE":
		n.gone = append(n.gone, strings.TrimPrefix(r.URL.Path, "/v1/apps/"))
		w.WriteHeader(204)
	default:
		http.Error(w, `{"error":"unexpected"}`, 500)
	}
}

const shopCompose = `services:
  web:
    build: ./web
    ports: ["3000"]
    environment: {API_URL: "http://api:4000"}
    depends_on: [api]
  api:
    build: {context: ./api}
    env_file: api/.env
    environment: [DATABASE_URL=postgres://shop:${DB_PASSWORD}@db:5432/shop]
    expose: ["4000"]
    depends_on: [db]
  db:
    image: postgres:18
    environment: {POSTGRES_PASSWORD: "${DB_PASSWORD}"}
    volumes: [pgdata:/var/lib/postgresql/data]
  worker:
    image: me/worker
    command: node worker.js
volumes: {pgdata: {}}
`

func TestComposeSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
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
	db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen) VALUES ('n1', 'prod-1', 'prod', 'h', '100.64.0.1', ?, ?)`, now, now)
	db.Write.Exec(`INSERT INTO projects (id, name, description, created_at) VALUES ('p1', 'shop', '', ?), ('p2', 'other', '', ?)`, now, now)

	n := &node{repo: map[string]string{"docker-compose.yml": shopCompose, "api/.env": "LOG_LEVEL=info\n"}, specs: map[string]agent.AppSpec{}}
	srv := httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(srv.Close)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dial}, Timeout: 5 * time.Second}}
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	set := settings.NewService(db, box)
	nodes := fleet.NewService(fleet.NewStore(db))
	apps := app.NewService(app.NewStore(db), nodes, agents, box, set)
	go apps.Run(ctx)
	vars := func(context.Context, string) (envref.Vars, error) {
		return envref.Vars{"shared": {"DB_PASSWORD": "pw"}}, nil
	}
	svc := compose.NewService(db, apps, nodes, agents, vars, set.CloneToken)
	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		compose.NewHandler(svc).Routes(r)
		app.NewHandler(apps).Routes(r)
	})
	call := func(method, path string, body, out any) int {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
		if out != nil {
			json.NewDecoder(rec.Body).Decode(out)
		}
		return rec.Code
	}
	repo := compose.Source{RepoURL: "https://github.com/me/shop.git", Branch: "main"}

	if code := call("PUT", "/api/projects/default/compose", repo, nil); code != 400 {
		t.Errorf("the default project: %d, want 400", code)
	}
	var d compose.Diff
	if code := call("POST", "/api/projects/shop/compose/plan", repo, &d); code != 200 || strings.Join(d.Create, ",") != "shop-db,shop-api,shop-web,shop-worker" {
		t.Fatalf("plan: %d %+v", code, d)
	}
	if code := call("PUT", "/api/projects/shop/compose", repo, &d); code != 200 || len(d.Deployments) != 4 {
		t.Fatalf("apply: %d %+v", code, d)
	}
	waitDeployed(t, n, 4)
	api, dbs := n.spec("shop-api"), n.spec("shop-db")
	if api.Alias != "api" || api.Context != "api" || api.Health != "tcp" || api.Port != 4000 || api.Network != "walroos-p-shop" ||
		!hasAll(api.Env, "LOG_LEVEL=info", "DATABASE_URL=postgres://shop:pw@db:5432/shop") {
		t.Errorf("api = %+v", api)
	}
	if dbs.Source != "docker.io/library/postgres:18" || dbs.Alias != "db" || len(dbs.Volumes) != 1 || dbs.Volumes[0].Name != "pgdata" || dbs.Health != "running" {
		t.Errorf("db = %+v", dbs)
	}
	var saved compose.Source
	if call("GET", "/api/projects/shop/compose", nil, &saved); saved.RepoURL != repo.RepoURL {
		t.Errorf("saved = %+v", saved)
	}

	// The worker goes and the database changes version: the next sync updates and removes.
	n.mu.Lock()
	n.repo["docker-compose.yml"] = strings.Replace(strings.Replace(shopCompose, "postgres:18", "postgres:17", 1), "  worker:\n    image: me/worker\n    command: node worker.js\n", "", 1)
	n.mu.Unlock()
	if call("POST", "/api/projects/shop/compose/plan", repo, &d); strings.Join(d.Remove, ",") != "shop-worker" || len(d.Update) != 3 || len(d.Create) != 0 {
		t.Fatalf("second plan: %+v", d)
	}
	if code := call("PUT", "/api/projects/shop/compose", repo, &d); code != 200 || len(n.gone) != 1 {
		t.Fatalf("second apply: %d, removed %v", code, n.gone)
	}

	// A name taken outside the compose: refused before anything changes.
	if code := call("POST", "/api/apps", map[string]any{"name": "other-db", "image": "docker.io/library/redis:8", "port": 6379, "project": "other"}, nil); code != 201 {
		t.Fatalf("create other-db: %d", code)
	}
	pasted := compose.Source{Text: "services:\n  db: {image: redis}\n"}
	if code := call("POST", "/api/projects/other/compose/plan", pasted, nil); code != 409 {
		t.Errorf("a taken name: %d, want 409", code)
	}
	// A pasted file with a build: nothing to build from.
	if code := call("POST", "/api/projects/other/compose/plan", compose.Source{Text: "services:\n  web: {build: .}\n"}, nil); code != 400 {
		t.Errorf("a pasted build: %d, want 400", code)
	}
}

func (n *node) spec(name string) agent.AppSpec {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.specs[name]
}

func waitDeployed(t *testing.T, n *node, count int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		n.mu.Lock()
		got := len(n.specs)
		n.mu.Unlock()
		if got >= count {
			return
		}
	}
	t.Fatalf("only %d deployed", len(n.specs))
}

func hasAll(have []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			found = found || h == w
		}
		if !found {
			return false
		}
	}
	return true
}
