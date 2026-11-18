package app_test

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
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
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
}

func (f *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/deploy"):
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
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/logs"):
		w.Write([]byte("GET / 200\n"))
	case r.Method == "DELETE" && f.busy:
		http.Error(w, `{"error":"busy"}`, 409)
	case r.Method == "DELETE":
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/v1/apps/"))
		w.WriteHeader(204)
	default:
		http.Error(w, `{"error":"unexpected"}`, 500)
	}
}

type hub struct {
	http  http.Handler
	agent *fakeAgent
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
	svc := app.NewService(app.NewStore(db), fleet.NewService(fleet.NewStore(db)), agents, box)
	go svc.Run(ctx)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		app.NewHandler(svc).Routes(r)
	})
	return &hub{http: r, agent: fa}
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
	if spec.ID != d.ID || spec.RepoURL != a.RepoURL || spec.Port != 8000 || len(spec.Env) != 1 || spec.Env[0] != "DB_URL=postgres://x" {
		t.Errorf("agent got %+v", spec)
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
	if got.Last == nil || got.Last.ID != back.ID {
		t.Errorf("last = %+v", got.Last)
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
