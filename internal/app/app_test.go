package app_test

import (
	"bytes"
	"cmp"
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
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// fakeAgent answers the agent's app API: deploys stream two log lines, then the release (or
// the error in fail).
type fakeAgent struct {
	mu      sync.Mutex
	specs   []agent.AppSpec
	fail    string
	busy    bool // DELETE answers 409, as while a deploy runs
	deleted []string
	calls   []string // "<host> <op>" of each build and deploy
	builds  []agent.AppSpec
	tunnel  string // the token of the last PUT /v1/tunnel
	state   string // the release's ActiveState in GET /v1/apps; "" = active
	powers  []string
}

func (f *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "PUT" && r.URL.Path == "/v1/tunnel":
		var in struct{ Token string }
		json.NewDecoder(r.Body).Decode(&in)
		f.tunnel = in.Token
		w.WriteHeader(204)
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/build"):
		var spec agent.AppSpec
		json.NewDecoder(r.Body).Decode(&spec)
		f.builds = append(f.builds, spec)
		f.calls = append(f.calls, r.Host+" build")
		enc := json.NewEncoder(w)
		enc.Encode(agent.DeployLine{Log: "pushing"})
		if f.fail != "" {
			enc.Encode(agent.DeployLine{Error: f.fail})
			return
		}
		enc.Encode(agent.DeployLine{Release: &agent.Release{Sha: "abc1234567890", Image: "localhost/walroos-app-" + spec.Name + ":abc123456789"}})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/deploy"):
		f.calls = append(f.calls, r.Host+" deploy")
		var spec agent.AppSpec
		json.NewDecoder(r.Body).Decode(&spec)
		f.specs = append(f.specs, spec)
		enc := json.NewEncoder(w)
		enc.Encode(agent.DeployLine{Log: "building " + spec.Name})
		enc.Encode(agent.DeployLine{Log: "health check: 200"})
		if f.fail != "" {
			enc.Encode(agent.DeployLine{Error: f.fail})
			return
		}
		sha, image := "abc123", "localhost/walroos-app-"+spec.Name+":abc123"
		if spec.Image != "" {
			image = spec.Image
		}
		enc.Encode(agent.DeployLine{Release: &agent.Release{Sha: sha, Image: image, Container: spec.Name + "-" + spec.ID}})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/power"):
		var in struct{ Action string }
		json.NewDecoder(r.Body).Decode(&in)
		switch {
		case f.busy:
			http.Error(w, `{"error":"busy"}`, 409)
		case len(f.specs) == 0:
			http.Error(w, `{"error":"app not found on this node"}`, 404)
		default:
			f.powers = append(f.powers, in.Action)
			f.state = map[string]string{"stop": "inactive"}[in.Action]
			w.WriteHeader(204)
		}
	case r.Method == "GET" && r.URL.Path == "/v1/apps":
		var list []agent.App
		if n := len(f.specs); n > 0 && f.fail == "" {
			s := f.specs[n-1]
			list = append(list, agent.App{Name: s.Name, Container: s.Name + "-" + s.ID, State: cmp.Or(f.state, "active")})
		}
		json.NewEncoder(w).Encode(append([]agent.App{}, list...))
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/logs"):
		w.Write([]byte("GET / 200\n"))
	case r.Method == "DELETE" && f.busy:
		http.Error(w, `{"error":"busy"}`, 409)
	case r.Method == "DELETE":
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.RequestURI(), "/v1/apps/"))
		w.WriteHeader(204)
	default:
		http.Error(w, `{"error":"unexpected"}`, 500)
	}
}

type hub struct {
	http  http.Handler
	agent *fakeAgent
	db    *config.Database
	svc   *app.Service
	set   *settings.Service
}

func newHub(t *testing.T, roles string) *hub {
	t.Helper()
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
	if _, err := db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES ('n1', 'prod-1', ?, 'h', '100.64.0.1', ?, ?)`, roles, now, now); err != nil {
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
	set := settings.NewService(db, box)
	svc := app.NewService(app.NewStore(db), fleet.NewService(fleet.NewStore(db)), agents, box, set)
	go svc.Run(ctx)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		app.NewHandler(svc).Routes(r)
		settings.NewHandler(set).Routes(r)
	})
	return &hub{http: r, agent: fa, db: db, svc: svc, set: set}
}

func (h *hub) call(t *testing.T, method, path string, body, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	if out != nil {
		json.NewDecoder(rec.Body).Decode(out)
	}
	return rec.Code
}

// wait polls the deployment until the worker finished it.
func (h *hub) wait(t *testing.T, name, id string) app.Deployment {
	t.Helper()
	for range 100 {
		var d app.Deployment
		h.call(t, "GET", "/api/apps/"+name+"/deployments/"+id, nil, &d)
		if d.State == "succeeded" || d.State == "failed" {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("deployment %s never finished", id)
	return app.Deployment{}
}

func TestAppDeployFlow(t *testing.T) {
	h := newHub(t, "prod")
	var a app.App
	if code := h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://example.com/web.git", Port: 8000}, &a); code != 201 || a.NodeName != "prod-1" {
		t.Fatalf("create: %d %+v", code, a)
	}
	if code := h.call(t, "PUT", "/api/apps/web/env", []map[string]any{{"key": "DB_URL", "value": "postgres://x", "secret": true}}, nil); code != 200 {
		t.Fatalf("env: %d", code)
	}

	var d app.Deployment
	if code := h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d); code != 202 || d.State != "queued" {
		t.Fatalf("deploy: %d %+v", code, d)
	}
	v1 := h.wait(t, "web", d.ID)
	if v1.State != "succeeded" || v1.Sha != "abc123" || v1.Log != "building web\nhealth check: 200\n" {
		t.Fatalf("v1 = %+v", v1)
	}
	spec := h.agent.specs[0]
	if spec.ID != d.ID || spec.RepoURL != a.RepoURL || spec.Port != 8000 || len(spec.Env) != 1 || spec.Env[0] != "DB_URL=postgres://x" ||
		a.Project != "default" || spec.Network != agent.AppNetwork {
		t.Errorf("agent got %+v (project %q)", spec, a.Project)
	}

	// A failed deploy is recorded with its log; it can't be rolled back to.
	h.agent.fail = "health check: no 2xx"
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{"sha": "def4567"}, &d)
	bad := h.wait(t, "web", d.ID)
	if bad.State != "failed" || bad.Error != "health check: no 2xx" || bad.Sha != "def4567" || !strings.Contains(bad.Log, "building") {
		t.Fatalf("bad = %+v", bad)
	}
	if code := h.call(t, "POST", "/api/apps/web/deployments/"+bad.ID+"/rollback", nil, nil); code != 409 {
		t.Errorf("rollback to failed: %d", code)
	}

	// Rollback re-runs v1's image, no build.
	h.agent.fail = ""
	h.call(t, "POST", "/api/apps/web/deployments/"+v1.ID+"/rollback", nil, &d)
	back := h.wait(t, "web", d.ID)
	if back.State != "succeeded" || h.agent.specs[2].Image != v1.Image || back.Image != v1.Image {
		t.Fatalf("rollback = %+v, spec %+v", back, h.agent.specs[2])
	}

	var list []app.Deployment
	h.call(t, "GET", "/api/apps/web/deployments", nil, &list)
	if len(list) != 3 || list[0].ID != back.ID || list[0].Log != "" {
		t.Errorf("deployments = %+v", list)
	}
	var got app.App
	h.call(t, "GET", "/api/apps/web", nil, &got)
	if got.Last == nil || got.Last.ID != back.ID || got.NodeDown || got.Running == nil || got.Running.Container != "web-"+back.ID {
		t.Errorf("last = %+v, running = %+v, node down %v", got.Last, got.Running, got.NodeDown)
	}
	var all []app.Deployment
	h.call(t, "GET", "/api/deployments", nil, &all)
	if len(all) != 3 || all[0].AppName != "web" {
		t.Errorf("all deployments = %+v", all)
	}

	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, httptest.NewRequest("GET", "/api/apps/web/logs", nil))
	if rec.Body.String() != "GET / 200\n" {
		t.Errorf("logs = %q", rec.Body.String())
	}

	if code := h.call(t, "DELETE", "/api/apps/web", nil, nil); code != 204 || len(h.agent.deleted) != 1 || h.agent.deleted[0] != "web" {
		t.Fatalf("delete: %d %v", code, h.agent.deleted)
	}
	if code := h.call(t, "GET", "/api/apps/web", nil, nil); code != 404 {
		t.Errorf("after delete: %d", code)
	}
}

func TestAppErrors(t *testing.T) {
	h := newHub(t, "prod")
	for _, in := range []app.CreateInput{
		{Name: "Bad", RepoURL: "https://x/y", Port: 80},
		{Name: "ok", RepoURL: "/etc", Port: 80},
		{Name: "ok", RepoURL: "file:///etc", Port: 80},
		{Name: "ok", RepoURL: "https://x/y"},
		{Name: "ok", RepoURL: "https://x/y", Port: 80, Branch: "--upload-pack=touch /tmp/x"},
		{Name: "ok", RepoURL: "https://x/y", Port: 80, Branch: "main\nfake log line"},
		{Name: "ok", RepoURL: "https://x/y", Port: 80, Dockerfile: "-f/etc/passwd"},
		{Name: "ok", RepoURL: "https://x/y", Port: 80, HealthPath: "/ HTTP/1.0\r\nX: y\r\n\r\nGET /x"}, // request and log injection
		{Name: "ok", RepoURL: "https://x/y", Port: 80, HealthPath: "health"},
	} {
		if code := h.call(t, "POST", "/api/apps", in, nil); code != 400 {
			t.Errorf("%+v: %d", in, code)
		}
	}
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://x/y", Port: 80}, nil)
	if code := h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://x/y", Port: 80}, nil); code != 409 {
		t.Errorf("duplicate: %d", code)
	}
	if code := h.call(t, "PUT", "/api/apps/web/env", []map[string]any{{"key": "bad key"}}, nil); code != 400 {
		t.Errorf("bad env: %d", code)
	}
	for _, sha := range []string{"--orphan=x", "main", "abc"} { // option injection into git checkout
		if code := h.call(t, "POST", "/api/apps/web/deploy", map[string]string{"sha": sha}, nil); code != 400 {
			t.Errorf("sha %q: %d", sha, code)
		}
	}
	h.agent.busy = true
	if code := h.call(t, "DELETE", "/api/apps/web", nil, nil); code != 409 {
		t.Errorf("delete while the node deploys: %d", code)
	}
	if code := h.call(t, "POST", "/api/apps/nope/deploy", nil, nil); code != 404 {
		t.Errorf("deploy unknown: %d", code)
	}
	if code := h.call(t, "GET", "/api/apps/web/deployments/nope", nil, nil); code != 404 {
		t.Errorf("unknown deployment: %d", code)
	}

	dev := newHub(t, "dev")
	if code := dev.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://x/y", Port: 80}, nil); code != 409 {
		t.Errorf("no prod node: %d", code)
	}
	if code := dev.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://x/y", Port: 80, Node: "prod-1"}, nil); code != 409 {
		t.Errorf("node without prod role: %d", code)
	}
}

// With a build node, the build runs there and the prod node pulls it from its registry.
func TestAppBuildNode(t *testing.T) {
	h := newHub(t, "prod")
	now := time.Now().UTC()
	if _, err := h.db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES ('b1', 'builder', 'build', 'hb', '100.64.0.9', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://example.com/web.git", Port: 8000}, nil)
	h.call(t, "PUT", "/api/apps/web/env", []map[string]any{{"key": "DB_URL", "value": "postgres://x"}}, nil)

	var d app.Deployment
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	v1 := h.wait(t, "web", d.ID)
	if v1.State != "succeeded" || v1.Log != "building on builder\npushing\ndeploying on prod-1\nbuilding web\nhealth check: 200\n" {
		t.Fatalf("v1 = %+v", v1)
	}
	if strings.Join(h.agent.calls, ",") != "100.64.0.9:7070 build,100.64.0.1:7070 deploy" {
		t.Fatalf("calls = %v", h.agent.calls)
	}
	if b := h.agent.builds[0]; b.Env != nil || b.RepoURL != "https://example.com/web.git" || b.Pull != "" {
		t.Errorf("build spec = %+v", b) // secrets are for the running app, not the build node
	}
	if p := h.agent.specs[0]; p.Pull != "100.64.0.9:5000" || p.PullToken != agent.PullToken("hb", "web") || p.Sha != "abc1234567890" || len(p.Env) != 1 {
		t.Errorf("deploy spec = %+v", p)
	}

	// Rollback runs the image already on the prod node: no build.
	h.call(t, "POST", "/api/apps/web/deployments/"+v1.ID+"/rollback", nil, &d)
	if back := h.wait(t, "web", d.ID); back.State != "succeeded" || len(h.agent.builds) != 1 || h.agent.specs[1].Pull != "" {
		t.Fatalf("rollback = %+v, builds %d", back, len(h.agent.builds))
	}

	// A failed build never reaches the prod node.
	h.agent.fail = "podman build: exit 1"
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	if bad := h.wait(t, "web", d.ID); bad.State != "failed" || bad.Error != "build on builder: podman build: exit 1" || len(h.agent.specs) != 2 {
		t.Fatalf("failed build = %+v, deploys %d", bad, len(h.agent.specs))
	}

	// A prod node that also builds builds for itself.
	h.db.Write.Exec(`UPDATE nodes SET roles = 'build,prod' WHERE id = 'n1'`)
	h.agent.fail = ""
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	if h.wait(t, "web", d.ID); len(h.agent.builds) != 2 || h.agent.specs[2].Pull != "" {
		t.Fatalf("builds %d, spec %+v", len(h.agent.builds), h.agent.specs[2])
	}
}

func TestAppImage(t *testing.T) {
	h := newHub(t, "prod")
	now := time.Now().UTC()
	if _, err := h.db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES ('b1', 'builder', 'build', 'hb', '100.64.0.9', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []app.CreateInput{
		{Name: "x", Image: "ghcr.io/a/b:1", RepoURL: "https://example.com/x.git", Port: 80}, // both
		{Name: "x", Image: "ghcr.io/a/b:1", Branch: "main", Port: 80},                       // nothing to check out
		{Name: "x", Image: "python:3", Port: 80},                                            // short name: registry lookup
		{Name: "x", Image: "localhost/walroos-app-web:abc", Port: 80},                       // another app's build
		{Name: "x", Image: "ghcr.io/a/b:1 --privileged", Port: 80},
		{Name: "x", Image: "ghcr.io/-a/b", Port: 80},
		{Name: "x", Port: 80}, // neither
	} {
		if code := h.call(t, "POST", "/api/apps", bad, nil); code != 400 {
			t.Errorf("create %+v: %d", bad, code)
		}
	}
	var a app.App
	if code := h.call(t, "POST", "/api/apps", app.CreateInput{Name: "img", Image: "docker.io/library/python:3-alpine", Port: 8000}, &a); code != 201 || a.Image != "docker.io/library/python:3-alpine" {
		t.Fatalf("create: %d %+v", code, a)
	}
	if code := h.call(t, "POST", "/api/apps/img/deploy", map[string]string{"sha": "abc1234"}, nil); code != 400 {
		t.Errorf("deploy an image app at a sha: %d", code)
	}
	var d app.Deployment
	h.call(t, "POST", "/api/apps/img/deploy", map[string]string{}, &d)
	v1 := h.wait(t, "img", d.ID)
	if s := h.agent.specs[0]; v1.State != "succeeded" || s.Source != "docker.io/library/python:3-alpine" || s.RepoURL != "" || s.Pull != "" || len(h.agent.builds) != 0 {
		t.Fatalf("v1 = %+v, spec %+v, builds %d", v1, s, len(h.agent.builds)) // pulled on the prod node, never built
	}
	h.call(t, "POST", "/api/apps/img/deployments/"+v1.ID+"/rollback", nil, &d)
	if back := h.wait(t, "img", d.ID); back.State != "succeeded" || h.agent.specs[1].Image != v1.Image {
		t.Fatalf("rollback = %+v, spec %+v", back, h.agent.specs[1])
	}
}

func TestAppVolumes(t *testing.T) {
	h := newHub(t, "prod")
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "db", Image: "docker.io/library/postgres:17", Port: 5432}, nil)
	vols := []agent.Volume{{Name: "data", Target: "/var/lib/postgresql/data"}, {HostPath: "photos/library", Target: "/library", RO: true}}
	var a app.App
	if code := h.call(t, "PUT", "/api/apps/db/volumes", vols, &a); code != 200 || len(a.Volumes) != 2 || a.Volumes[1] != vols[1] {
		t.Fatalf("set: %d %+v", code, a.Volumes)
	}
	for _, bad := range [][]agent.Volume{
		{{Name: "x", HostPath: "x", Target: "/x"}}, // both
		{{Target: "/x"}}, // neither
		{{HostPath: "../home", Target: "/x"}},
		{{HostPath: "a/../../b", Target: "/x"}},
		{{HostPath: "/etc", Target: "/x"}}, // absolute: only under the data root
		{{HostPath: "a:b", Target: "/x"}},
		{{HostPath: ".", Target: "/x"}}, // the whole root
		{{Name: "Data", Target: "/x"}},
		{{Name: "x", Target: "relative"}},
		{{Name: "x", Target: "/"}},
		{{Name: "x", Target: "/a,ro"}},
		{{Name: "x", Target: "/a"}, {Name: "y", Target: "/a"}},
	} {
		if code := h.call(t, "PUT", "/api/apps/db/volumes", bad, nil); code != 400 {
			t.Errorf("volumes %+v: %d", bad, code)
		}
	}
	if h.call(t, "GET", "/api/apps/db", nil, &a); len(a.Volumes) != 2 {
		t.Errorf("a rejected list changed them: %+v", a.Volumes)
	}

	var d app.Deployment
	h.call(t, "POST", "/api/apps/db/deploy", map[string]string{}, &d)
	if h.wait(t, "db", d.ID); len(h.agent.specs[0].Volumes) != 2 || h.agent.specs[0].Volumes[0] != vols[0] {
		t.Fatalf("deploy spec volumes: %+v", h.agent.specs[0].Volumes)
	}
	if code := h.call(t, "DELETE", "/api/apps/db?purge=1", nil, nil); code != 204 || strings.Join(h.agent.deleted, ",") != "db?purge=1" {
		t.Fatalf("purge: %d %v", code, h.agent.deleted)
	}
}

// An app in a project deploys on that project's network; one without stays on the shared one.
func TestAppProjectNetwork(t *testing.T) {
	h := newHub(t, "prod")
	h.db.Write.Exec(`INSERT INTO projects (id, name, created_at) VALUES ('p1', 'shop', ?)`, time.Now().UTC())
	var a app.App
	if code := h.call(t, "POST", "/api/apps", app.CreateInput{Name: "api", RepoURL: "https://example.com/api.git", Port: 8000, Project: "shop"}, &a); code != 201 || a.Project != "shop" {
		t.Fatalf("create in shop: %d %+v", code, a)
	}
	var d app.Deployment
	h.call(t, "POST", "/api/apps/api/deploy", map[string]string{}, &d)
	h.wait(t, "api", d.ID)
	if got := h.agent.specs[0].Network; got != "walroos-p-shop" {
		t.Errorf("network = %q, want walroos-p-shop", got)
	}
}

// Like Dokploy, an app can be created with just a name (a draft), get its source later, then deploy.
func TestAppDraftThenSource(t *testing.T) {
	h := newHub(t, "prod")
	var a app.App
	if code := h.call(t, "POST", "/api/apps", map[string]string{"name": "api", "description": "the backend"}, &a); code != 201 || a.RepoURL != "" || a.Description != "the backend" {
		t.Fatalf("draft: %d %+v", code, a)
	}
	if code := h.call(t, "POST", "/api/apps/api/deploy", map[string]string{}, nil); code != 400 {
		t.Errorf("deploy a draft: %d, want 400", code)
	}
	// Volumes can be set before the source.
	if code := h.call(t, "PUT", "/api/apps/api/volumes", []map[string]string{{"name": "data", "target": "/data"}}, nil); code != 200 {
		t.Errorf("volumes on a draft: %d", code)
	}
	for _, bad := range []map[string]any{
		{"port": 3000},
		{"repo_url": "/etc", "port": 3000},
		{"repo_url": "https://github.com/me/api", "port": 0},
		{"repo_url": "https://github.com/me/api", "image": "ghcr.io/me/api:1", "port": 3000},
		{"image": "api:latest", "port": 3000},
	} {
		if code := h.call(t, "PUT", "/api/apps/api/source", bad, nil); code != 400 {
			t.Errorf("source %v: %d, want 400", bad, code)
		}
	}
	if code := h.call(t, "PUT", "/api/apps/api/source", map[string]any{"repo_url": "https://github.com/me/api", "branch": "main", "port": 3000}, &a); code != 200 || a.RepoURL != "https://github.com/me/api" || a.Port != 3000 {
		t.Fatalf("set source: %d %+v", code, a)
	}
	var d app.Deployment
	if code := h.call(t, "POST", "/api/apps/api/deploy", map[string]string{}, &d); code != 202 {
		t.Fatalf("deploy after source: %d", code)
	}
	if got := h.wait(t, "api", d.ID); got.State != "succeeded" || h.agent.specs[0].RepoURL != "https://github.com/me/api" || len(h.agent.specs[0].Volumes) != 1 {
		t.Errorf("deployment %+v, spec %+v", got, h.agent.specs[0])
	}
	// Switch it to a published image.
	if code := h.call(t, "PUT", "/api/apps/api/source", map[string]any{"image": "ghcr.io/me/api:2", "port": 8080}, &a); code != 200 || a.RepoURL != "" || a.Image != "ghcr.io/me/api:2" {
		t.Errorf("switch to image: %d %+v", code, a)
	}
	if code := h.call(t, "POST", "/api/apps", map[string]string{"name": "x", "description": strings.Repeat("a", 501)}, nil); code != 400 {
		t.Errorf("long description: %d", code)
	}
}

// A static site: no Dockerfile, its port is the static server's, and its build (on a build node
// too) gets the app's env vars, which its bundle needs.
func TestAppStaticSite(t *testing.T) {
	h := newHub(t, "prod")
	for _, bad := range []map[string]any{
		{"image": "ghcr.io/me/web:1", "static": map[string]any{}},
		{"repo_url": "https://github.com/me/web", "dockerfile": "Dockerfile", "static": map[string]any{}},
		{"repo_url": "https://github.com/me/web", "static": map[string]any{"runtime": "deno"}},
		{"repo_url": "https://github.com/me/web", "static": map[string]any{"publish": "../etc"}},
		{"repo_url": "https://github.com/me/web", "static": map[string]any{"publish": "/srv"}},
	} {
		bad["name"] = "web"
		if code := h.call(t, "POST", "/api/apps", bad, nil); code != 400 {
			t.Errorf("create %v: %d, want 400", bad, code)
		}
	}
	var a app.App
	in := map[string]any{"name": "web", "repo_url": "https://github.com/me/web", "port": 3000,
		"static": map[string]any{"runtime": "bun", "build": "bun run build", "publish": "dist", "spa": true}}
	if code := h.call(t, "POST", "/api/apps", in, &a); code != 201 || a.Port != agent.StaticPort || a.Static == nil || a.Static.Runtime != "bun" || !a.Static.SPA {
		t.Fatalf("create: %d %+v", code, a)
	}
	h.call(t, "PUT", "/api/apps/web/env", []map[string]any{{"key": "VITE_API_URL", "value": "https://api.example.com"}}, nil)

	now := time.Now().UTC()
	if _, err := h.db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES ('b1', 'builder', 'build', 'hb', '100.64.0.9', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	var d app.Deployment
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	if got := h.wait(t, "web", d.ID); got.State != "succeeded" {
		t.Fatalf("deploy: %+v", got)
	}
	if b := h.agent.builds[0]; b.Static == nil || b.Static.Publish != "dist" || len(b.Env) != 1 || b.Port != agent.StaticPort {
		t.Errorf("build spec = %+v", b)
	}

	// Back to a Dockerfile: the static settings go.
	var b app.App
	if code := h.call(t, "PUT", "/api/apps/web/source", map[string]any{"repo_url": "https://github.com/me/web", "port": 3000}, &b); code != 200 || b.Static != nil || b.Port != 3000 {
		t.Errorf("to a Dockerfile: %d %+v", code, b)
	}
}

// A template fills in image, port, health path, volumes and generated secrets, then deploys.
func TestAppFromTemplate(t *testing.T) {
	h := newHub(t, "prod")
	var list []app.Template
	if h.call(t, "GET", "/api/templates", nil, &list); len(list) < 10 {
		t.Fatalf("templates = %d", len(list))
	}
	var a app.App
	if code := h.call(t, "POST", "/api/apps/from-template", map[string]string{"template": "grafana", "name": "dash"}, &a); code != 201 {
		t.Fatalf("from template: %d", code)
	}
	if a.Image != "docker.io/grafana/grafana:latest" || a.Port != 3000 || a.HealthPath != "/api/health" || len(a.Volumes) != 1 || a.Description == "" {
		t.Errorf("app = %+v", a)
	}
	var env []workspace.EnvVar
	h.call(t, "GET", "/api/apps/dash/env", nil, &env)
	if len(env) != 1 || env[0].Key != "GF_SECURITY_ADMIN_PASSWORD" || len(env[0].Value) < 20 || !env[0].Secret {
		t.Errorf("env = %+v", env)
	}
	var deps []app.Deployment
	h.call(t, "GET", "/api/apps/dash/deployments", nil, &deps)
	if len(deps) != 1 {
		t.Fatalf("deployments = %+v, want the first one queued", deps)
	}
	h.wait(t, "dash", deps[0].ID)
	if spec := h.agent.specs[0]; spec.Source != a.Image || spec.Volumes[0].Target != "/var/lib/grafana" || spec.Env[0] != "GF_SECURITY_ADMIN_PASSWORD="+env[0].Value {
		t.Errorf("deployed spec = %+v", spec)
	}
	for body, want := range map[string]int{`{"template":"nope","name":"x"}`: 404, `{"template":"nginx","name":"Bad Name"}`: 400, `{"template":"nginx","name":"dash"}`: 409, `{"template":"nginx","name":"from-template"}`: 400} {
		var m map[string]string
		json.Unmarshal([]byte(body), &m)
		if code := h.call(t, "POST", "/api/apps/from-template", m, nil); code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
}

// Stop, start and restart reach the node; an app with no release yet can't, nor one mid-deploy.
func TestAppPower(t *testing.T) {
	h := newHub(t, "prod")
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://example.com/web.git", Port: 8000}, nil)
	if code := h.call(t, "POST", "/api/apps/web/power", map[string]string{"action": "stop"}, nil); code != 409 {
		t.Errorf("stop before a deploy: %d, want 409", code)
	}
	var d app.Deployment
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	h.wait(t, "web", d.ID)

	var a app.App
	if code := h.call(t, "POST", "/api/apps/web/power", map[string]string{"action": "stop"}, &a); code != 200 || a.Running == nil || a.Running.State != "inactive" {
		t.Fatalf("stop: %d %+v", code, a.Running)
	}
	if code := h.call(t, "POST", "/api/apps/web/power", map[string]string{"action": "restart"}, &a); code != 200 || a.Running.State != "active" {
		t.Fatalf("restart: %d %+v", code, a.Running)
	}
	if code := h.call(t, "POST", "/api/apps/web/power", map[string]string{"action": "pause"}, nil); code != 400 {
		t.Errorf("unknown action: %d, want 400", code)
	}
	h.agent.busy = true
	if code := h.call(t, "POST", "/api/apps/web/power", map[string]string{"action": "start"}, nil); code != 409 {
		t.Errorf("mid-deploy: %d, want 409", code)
	}
	if strings.Join(h.agent.powers, ",") != "stop,restart" {
		t.Errorf("node got %v", h.agent.powers)
	}
}

// With a GitHub token saved, a github.com repo's deploy carries it for the clone (a private repo
// needs it); a repo elsewhere doesn't get it.
func TestAppCloneToken(t *testing.T) {
	h := newHub(t, "prod")
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login": "octo"}`)) // fine-grained: no X-OAuth-Scopes
	}))
	defer gh.Close()
	h.set.GitHubAPI = gh.URL
	if _, err := h.set.SetGitHub(context.Background(), "github_pat_x"); err != nil {
		t.Fatal(err)
	}
	for name, repo := range map[string]string{"private": "git@github.com:octo/private.git", "elsewhere": "https://gitlab.com/octo/app.git"} {
		h.call(t, "POST", "/api/apps", app.CreateInput{Name: name, RepoURL: repo, Port: 8000}, nil)
		var d app.Deployment
		h.call(t, "POST", "/api/apps/"+name+"/deploy", map[string]string{}, &d)
		h.wait(t, name, d.ID)
	}
	got := map[string]string{}
	for _, s := range h.agent.specs {
		got[s.Name] = s.GitToken
	}
	if got["private"] != "github_pat_x" || got["elsewhere"] != "" {
		t.Fatalf("tokens = %v", got)
	}
}
