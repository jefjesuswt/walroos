package workspace_test

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
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// fakeAgent is a node agent that keeps workspaces in memory: the hub's HTTP contract, no podman.
type fakeAgent struct {
	mu   sync.Mutex
	ws   map[string]agent.Workspace
	down bool   // answer 500 to everything
	fail string // if set, POST /v1/workspaces answers 400 with it
	last agent.WorkspaceSpec
	env  []string // last env the hub sent
}

func newFakeAgent(t *testing.T) (*fakeAgent, *httptest.Server) {
	t.Helper()
	fa := &fakeAgent{ws: map[string]agent.Workspace{}}
	srv := httptest.NewServer(http.HandlerFunc(fa.serve))
	t.Cleanup(srv.Close)
	return fa, srv
}

func (f *fakeAgent) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, `{"error":"agent down"}`, 500)
		return
	}
	json := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	errJSON := func(code int, msg string) { json(code, map[string]string{"error": msg}) }

	path := strings.TrimPrefix(r.URL.Path, "/v1/workspaces")
	name := strings.TrimPrefix(strings.TrimPrefix(path, "/"), "/")
	sub := ""
	if i := strings.Index(name, "/"); i >= 0 {
		name, sub = name[:i], name[i+1:]
	}

	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/detect":
		var body struct {
			RepoURL string `json:"repo_url"`
			Branch  string `json:"branch"`
		}
		if err := jsonDecode(r, &body); err != nil || strings.Contains(body.RepoURL, "missing") {
			errJSON(422, "can't clone the repo: not found")
			return
		}
		json(200, map[string]any{"stack": "go", "branch": cmpOr(body.Branch, "main"), "devcontainer": map[string]string{"image": "go"}})
	case r.Method == "GET" && path == "":
		list := []agent.Workspace{}
		for _, ws := range f.ws {
			list = append(list, ws)
		}
		json(200, list)
	case r.Method == "POST" && path == "":
		var spec struct {
			agent.WorkspaceSpec
			Env []string `json:"env"`
		}
		if err := jsonDecode(r, &spec); err != nil {
			errJSON(400, "invalid JSON body")
			return
		}
		f.last = spec.WorkspaceSpec
		f.env = spec.Env
		if f.fail != "" {
			errJSON(400, f.fail)
			return
		}
		if _, ok := f.ws[spec.Name]; ok {
			errJSON(409, "workspace already exists on this node")
			return
		}
		f.ws[spec.Name] = agent.Workspace{Name: spec.Name, State: "creating"}
		json(202, f.ws[spec.Name])
	case r.Method == "GET" && name != "" && sub == "":
		ws, ok := f.ws[name]
		if !ok {
			errJSON(404, "workspace not found")
			return
		}
		json(200, ws)
	case r.Method == "POST" && sub == "start":
		ws, ok := f.ws[name]
		if !ok {
			errJSON(404, "workspace not found")
			return
		}
		ws.State = "running"
		f.ws[name] = ws
		json(200, ws)
	case r.Method == "POST" && sub == "stop":
		ws, ok := f.ws[name]
		if !ok {
			errJSON(404, "workspace not found")
			return
		}
		ws.State = "stopped"
		f.ws[name] = ws
		json(200, ws)
	case r.Method == "POST" && sub == "rebuild":
		var body struct{ Env []string }
		jsonDecode(r, &body)
		f.env = body.Env
		ws, ok := f.ws[name]
		if !ok {
			errJSON(404, "workspace not found")
			return
		}
		ws.State = "creating"
		f.ws[name] = ws
		json(202, ws)
	case r.Method == "DELETE" && name != "":
		if _, ok := f.ws[name]; !ok {
			errJSON(404, "workspace not found")
			return
		}
		delete(f.ws, name)
		w.WriteHeader(204)
	default:
		errJSON(404, "not found")
	}
}

func (f *fakeAgent) set(name string, ws agent.Workspace) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ws[name] = ws
}

func jsonDecode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

type hub struct {
	http    http.Handler
	db      *config.Database
	agent   *fakeAgent
	nodeID  string
	login   string
	agentsC *agent.Client
}

// newHub wires the same routes as `walroos hub`, on a temp SQLite, with the caller as owner.
// Every node address dials the fake agent.
func newHub(t *testing.T, withDevNode bool) *hub {
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
	if err := users.EnsureOwner(ctx, "owner@github"); err != nil {
		t.Fatal(err)
	}
	fa, srv := newFakeAgent(t)

	h := &hub{db: db, agent: fa, login: "owner@github"}
	if withDevNode {
		h.nodeID = addNode(t, db, "homelab-1", "dev")
	}
	// The hub dials any node address through the fake agent.
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	h.agentsC = &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dial}}}

	fleetSvc := fleet.NewService(fleet.NewStore(db))
	fleetHandler := fleet.NewHandler(fleetSvc, "http://walroos-hub", "nHub", h.agentsC)
	box, err := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	wsHandler := workspace.NewHandler(workspace.NewService(workspace.NewStore(db), fleetSvc, h.agentsC, box))

	r := whttp.NewRouter()
	fleetHandler.NodeRoutes(r)
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static(h.login), users))
		r.Get("/api/me", auth.HandleMe)
		fleetHandler.UserRoutes(r)
		wsHandler.Routes(r)
	})
	h.http = r
	return h
}

func addNode(t *testing.T, db *config.Database, name string, roles ...string) string {
	t.Helper()
	id := name + "-id"
	now := time.Now().UTC()
	if _, err := db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES (?, ?, ?, ?, '100.64.0.1', ?, ?)`, id, name, strings.Join(roles, ","), "h-"+id, now, now); err != nil {
		t.Fatal(err)
	}
	return id
}

// callErr is call, returning the error message for any status.
func (h *hub) callErr(t *testing.T, method, path string, body any) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, req)
	var e struct{ Error string }
	json.NewDecoder(rec.Body).Decode(&e)
	return rec.Code, e.Error
}

func (h *hub) call(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = "100.101.12.4:41000"
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return rec.Code
}

func TestWorkspaceLifecycle(t *testing.T) {
	h := newHub(t, true)

	var got workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://github.com/me/app"}, &got); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if got.State != "creating" || got.Owner != h.login || got.NodeName != "homelab-1" {
		t.Fatalf("created = %+v", got)
	}
	if spec := h.agent.last; spec.Name != "app" || spec.RepoURL != "https://github.com/me/app" {
		t.Fatalf("agent got %+v", spec)
	}

	// The agent finishes creating; a list refreshes the row from it.
	h.agent.set("app", agent.Workspace{Name: "app", State: "running", Image: "localhost/walroos-base:abc"})
	var list []workspace.Workspace
	if code := h.call(t, "GET", "/api/workspaces", nil, &list); code != 200 || len(list) != 1 {
		t.Fatalf("list: %d %+v", code, list)
	}
	if list[0].State != "running" || list[0].Image != "localhost/walroos-base:abc" {
		t.Fatalf("list didn't refresh from the agent: %+v", list[0])
	}
	var stored string
	h.db.Read.QueryRow(`SELECT state FROM workspaces WHERE name = 'app'`).Scan(&stored)
	if stored != "running" {
		t.Errorf("row state = %q, want running (refresh must be saved)", stored)
	}

	// A name is unique hub-wide.
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://example.com/x"}, nil); code != 409 {
		t.Fatalf("duplicate: %d, want 409", code)
	}

	var ws workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces/app/stop", nil, &ws); code != 200 || ws.State != "stopped" {
		t.Fatalf("stop: %d %+v", code, ws)
	}
	if code := h.call(t, "POST", "/api/workspaces/app/start", nil, &ws); code != 200 || ws.State != "running" {
		t.Fatalf("start: %d %+v", code, ws)
	}

	// A node with workspaces can't be removed (the FK), and the API says why.
	if code := h.call(t, "DELETE", "/api/nodes/"+h.nodeID, nil, nil); code != 409 {
		t.Fatalf("delete node with workspaces: %d, want 409", code)
	}

	if code := h.call(t, "DELETE", "/api/workspaces/app", nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, ok := h.agent.ws["app"]; ok {
		t.Error("delete didn't reach the agent")
	}
	if code := h.call(t, "GET", "/api/workspaces/app", nil, nil); code != 404 {
		t.Fatalf("get after delete: %d", code)
	}
	if code := h.call(t, "DELETE", "/api/nodes/"+h.nodeID, nil, nil); code != 204 {
		t.Fatalf("delete node after its workspaces: %d", code)
	}
}

func TestWorkspaceErrors(t *testing.T) {
	h := newHub(t, true)

	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "Bad Name", "repo_url": "https://example.com/x"}, nil); code != 400 {
		t.Errorf("bad name: %d, want 400", code)
	}
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "ok"}, nil); code != 400 {
		t.Errorf("missing repo: %d, want 400", code)
	}
	if code := h.call(t, "GET", "/api/workspaces/ghost", nil, nil); code != 404 {
		t.Errorf("unknown: %d, want 404", code)
	}
	if code := h.call(t, "DELETE", "/api/workspaces/ghost", nil, nil); code != 404 {
		t.Errorf("delete unknown: %d, want 404", code)
	}

	// A workspace the agent already has there.
	h.agent.set("taken", agent.Workspace{Name: "taken", State: "running"})
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "taken", "repo_url": "https://example.com/x"}, nil); code != 409 {
		t.Errorf("agent says it exists: %d, want 409", code)
	}

	// An agent that rejects the spec.
	h.agent.fail = "boom"
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "bad", "repo_url": "https://example.com/x"}, nil); code != 400 {
		t.Errorf("agent 400: %d, want 400", code)
	}
	h.agent.fail = ""

	// An agent that's down.
	h.agent.down = true
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "down", "repo_url": "https://example.com/x"}, nil); code != 502 {
		t.Errorf("agent down: %d, want 502", code)
	}

}

func TestNoDevNode(t *testing.T) {
	h := newHub(t, false)
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://example.com/x"}, nil); code != 409 {
		t.Fatalf("no dev node: %d, want 409", code)
	}
	if _, msg := h.callErr(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://example.com/x"}); !strings.Contains(msg, "dev role") {
		t.Fatalf("error = %q, want it to mention the dev role", msg)
	}

	// A node without the dev role can't be asked, even by name.
	build := addNode(t, h.db, "builder", "build")
	if _, msg := h.callErr(t, "POST", "/api/workspaces", map[string]string{"name": "app2", "repo_url": "https://example.com/x", "node": "builder"}); !strings.Contains(msg, "dev role") {
		t.Fatalf("non-dev node: %q", msg)
	}
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app2", "repo_url": "https://example.com/x", "node": build}, nil); code != 409 {
		t.Fatalf("non-dev node by id: %d, want 409", code)
	}
}

// TestPicksLeastLoadedDevNode: with two dev nodes, a new workspace goes to the empty one,
// and asking for a node by name goes there even if it's busier.
func TestPicksLeastLoadedDevNode(t *testing.T) {
	h := newHub(t, true)                 // homelab-1 is the only one
	addNode(t, h.db, "homelab-2", "dev") // both online: addNode sets last_seen
	addNode(t, h.db, "builder", "build") // not a candidate

	var first workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "one", "repo_url": "https://example.com/x"}, &first); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var second workspace.Workspace
	h.call(t, "POST", "/api/workspaces", map[string]string{"name": "two", "repo_url": "https://example.com/x"}, &second)
	if first.NodeName == second.NodeName {
		t.Fatalf("both workspaces landed on %s; want the empty node used second", first.NodeName)
	}

	var byName workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "three", "repo_url": "https://example.com/x", "node": first.NodeName}, &byName); code != 201 || byName.NodeName != first.NodeName {
		t.Fatalf("create on a named node: %d %+v", code, byName)
	}
}

func TestWorkspaceRebuild(t *testing.T) {
	h := newHub(t, true)
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://example.com/x"}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var ws workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces/app/rebuild", nil, &ws); code != 202 || ws.State != "creating" {
		t.Fatalf("rebuild: %d %+v", code, ws)
	}
	if code := h.call(t, "POST", "/api/workspaces/ghost/rebuild", nil, nil); code != 404 {
		t.Fatalf("rebuild unknown: %d, want 404", code)
	}
}

func TestWorkspaceEnv(t *testing.T) {
	h := newHub(t, true)
	if code := h.call(t, "POST", "/api/workspaces", map[string]string{"name": "app", "repo_url": "https://example.com/x"}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}

	vars := []workspace.EnvVar{{Key: "DB_URL", Value: "postgres://u:p@db/app", Secret: true}, {Key: "LOG", Value: "debug"}}
	var got []workspace.EnvVar
	if code := h.call(t, "PUT", "/api/workspaces/app/env", vars, &got); code != 200 || len(got) != 2 {
		t.Fatalf("put env: %d %+v", code, got)
	}
	if got[0].Key != "DB_URL" || got[0].Value != "postgres://u:p@db/app" || !got[0].Secret {
		t.Fatalf("env = %+v", got)
	}

	// Stored encrypted, not as written.
	var stored string
	h.db.Read.QueryRow(`SELECT value FROM workspace_env WHERE key = 'DB_URL'`).Scan(&stored)
	if stored == "postgres://u:p@db/app" || !strings.HasPrefix(stored, "") || stored == "" {
		t.Fatalf("value stored in clear: %q", stored)
	}

	// Validation: bad name, duplicates, and unknown workspace.
	if code := h.call(t, "PUT", "/api/workspaces/app/env", []workspace.EnvVar{{Key: "bad-key", Value: "x"}}, nil); code != 400 {
		t.Errorf("bad key: %d, want 400", code)
	}
	if code := h.call(t, "PUT", "/api/workspaces/app/env", []workspace.EnvVar{{Key: "A", Value: "1"}, {Key: "A", Value: "2"}}, nil); code != 400 {
		t.Errorf("duplicate: %d, want 400", code)
	}
	if code := h.call(t, "PUT", "/api/workspaces/ghost/env", vars, nil); code != 404 {
		t.Errorf("unknown workspace: %d, want 404", code)
	}

	// Rebuild carries the env to the agent, and replace-on-PUT removes what's gone.
	if code := h.call(t, "POST", "/api/workspaces/app/rebuild", nil, nil); code != 202 {
		t.Fatalf("rebuild: %d", code)
	}
	if want := []string{"DB_URL=postgres://u:p@db/app", "LOG=debug"}; strings.Join(h.agent.env, ",") != strings.Join(want, ",") {
		t.Fatalf("agent env = %v, want %v", h.agent.env, want)
	}
	h.call(t, "PUT", "/api/workspaces/app/env", []workspace.EnvVar{{Key: "LOG", Value: "info"}}, nil)
	h.call(t, "GET", "/api/workspaces/app/env", nil, &got)
	if len(got) != 1 || got[0].Key != "LOG" || got[0].Value != "info" {
		t.Fatalf("after replace: %+v", got)
	}

	// Deleting the workspace removes its variables (FK cascade).
	h.call(t, "DELETE", "/api/workspaces/app", nil, nil)
	var n int
	h.db.Read.QueryRow(`SELECT COUNT(*) FROM workspace_env`).Scan(&n)
	if n != 0 {
		t.Errorf("%d env rows left after delete", n)
	}
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func TestWorkspaceDetect(t *testing.T) {
	h := newHub(t, true)
	var d struct {
		Stack, Branch string
		Devcontainer  map[string]string
	}
	if code := h.call(t, "POST", "/api/workspaces/detect", map[string]string{"repo_url": "https://github.com/me/app", "branch": "dev"}, &d); code != 200 {
		t.Fatalf("detect: %d", code)
	}
	if d.Stack != "go" || d.Branch != "dev" || d.Devcontainer["image"] != "go" {
		t.Fatalf("detect = %+v, want the agent's answer passed through", d)
	}
	if code, msg := h.callErr(t, "POST", "/api/workspaces/detect", map[string]string{"repo_url": "https://github.com/me/missing"}); code != 422 || !strings.Contains(msg, "not found") {
		t.Errorf("unreachable repo: %d %q, want 422 with the clone error", code, msg)
	}
	if code, _ := h.callErr(t, "POST", "/api/workspaces/detect", map[string]string{}); code != 400 {
		t.Errorf("no repo_url: %d, want 400", code)
	}
	h.agent.mu.Lock()
	h.agent.down = true
	h.agent.mu.Unlock()
	if code, _ := h.callErr(t, "POST", "/api/workspaces/detect", map[string]string{"repo_url": "https://example.com/x"}); code != 502 {
		t.Errorf("agent down: %d, want 502", code)
	}
	if code, _ := newHub(t, false).callErr(t, "POST", "/api/workspaces/detect", map[string]string{"repo_url": "https://example.com/x"}); code != 409 {
		t.Errorf("no dev node: %d, want 409", code)
	}
}

// TestWorkspaceCreateWithDevcontainer: the generated devcontainer reaches the agent untouched.
func TestWorkspaceCreateWithDevcontainer(t *testing.T) {
	h := newHub(t, true)
	dc := map[string]any{"image": "mcr.microsoft.com/devcontainers/go:1.27", "postCreateCommand": "go mod download"}
	in := map[string]any{"name": "app", "repo_url": "https://github.com/me/app", "devcontainer": dc}
	if code := h.call(t, "POST", "/api/workspaces", in, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var got map[string]any
	h.agent.mu.Lock()
	json.Unmarshal(h.agent.last.Devcontainer, &got)
	h.agent.mu.Unlock()
	if got["image"] != dc["image"] || got["postCreateCommand"] != dc["postCreateCommand"] {
		t.Fatalf("agent got devcontainer %v, want %v", got, dc)
	}
}

// The node's git clones repo_url: only remote URLs, never the node's own disk or ext:: commands.
func TestRepoURLMustBeRemote(t *testing.T) {
	h := newHub(t, true)
	for u, ok := range map[string]bool{
		"https://github.com/a/b":            true,
		"http://git.lan/a/b.git":            true,
		"ssh://git@gitlab.com:2222/a/b.git": true,
		"git://git.kernel.org/pub/x.git":    true,
		"git@github.com:a/b":                true,
		"/home/jeffrey/repo":                false,
		"file:///home/jeffrey/.ssh":         false,
		"../../repo":                        false,
		"ext::sh -c touch% /tmp/pwned":      false,
		"--upload-pack=touch /tmp/pwned":    false,
		"-x@host:path":                      false,
		"https://":                          false,
		"C:/Users/x/repo":                   false,
		"user@host:":                        false,
		"a/b@host:path":                     false,
	} {
		code, msg := h.callErr(t, "POST", "/api/workspaces/detect", map[string]string{"repo_url": u})
		if got := code != 400; got != ok {
			t.Errorf("detect %q: %d %s, want accepted=%v", u, code, msg, ok)
		}
		if !ok && !strings.Contains(msg, "remote git URL") {
			t.Errorf("detect %q: %s", u, msg)
		}
	}
	if code, msg := h.callErr(t, "POST", "/api/workspaces", map[string]string{"name": "x", "repo_url": "file:///home/jeffrey/.ssh"}); code != 400 || !strings.Contains(msg, "remote git URL") {
		t.Errorf("create with file://: %d %s", code, msg)
	}
}

// The stack detect found is stored for display; detect's catch-alls and unknown values aren't.
func TestWorkspaceStack(t *testing.T) {
	h := newHub(t, true)
	create := func(name, stack string) int {
		return h.call(t, "POST", "/api/workspaces", map[string]any{"name": name, "repo_url": "https://github.com/me/" + name, "stack": stack}, nil)
	}
	stackOf := func(name string) string {
		var w workspace.Workspace
		if code := h.call(t, "GET", "/api/workspaces/"+name, nil, &w); code != 200 {
			t.Fatalf("get %s: %d", name, code)
		}
		return w.Stack
	}

	for name, c := range map[string]struct{ in, want string }{
		"droid":   {"android", "android"},
		"api":     {"go", "go"},
		"own-dc":  {"devcontainer", ""}, // the repo's own devcontainer: the image says more
		"base":    {"none", ""},         // the walroos base image
		"no-hint": {"", ""},             // created without detect (old UI, API, MCP)
	} {
		if code := create(name, c.in); code != 201 {
			t.Fatalf("create %s with stack %q: %d", name, c.in, code)
		}
		if got := stackOf(name); got != c.want {
			t.Errorf("%s: stack %q stored as %q, want %q", name, c.in, got, c.want)
		}
	}

	// Unknown values are refused before the node starts creating anything.
	h.agent.mu.Lock()
	h.agent.last = agent.WorkspaceSpec{}
	h.agent.mu.Unlock()
	for _, bad := range []string{"Android", "cobol", "go ", "<script>", strings.Repeat("a", 300)} {
		if code, msg := h.callErr(t, "POST", "/api/workspaces", map[string]any{"name": "bad", "repo_url": "https://github.com/me/bad", "stack": bad}); code != 400 || !strings.Contains(msg, "stack must be") {
			t.Errorf("stack %q: %d %q, want 400", bad, code, msg)
		}
	}
	h.agent.mu.Lock()
	defer h.agent.mu.Unlock()
	if h.agent.last.Name != "" {
		t.Errorf("an invalid stack still reached the node: %+v", h.agent.last)
	}
}

// folder comes live from the node, so editors can open the repo (zed ssh://host/<folder>), and is
// simply absent when the node doesn't answer.
func TestWorkspaceFolder(t *testing.T) {
	h := newHub(t, true)
	if code := h.call(t, "POST", "/api/workspaces", map[string]any{"name": "app", "repo_url": "https://github.com/me/app"}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	h.agent.set("app", agent.Workspace{Name: "app", State: "running", Folder: "/workspaces/app"})
	var w workspace.Workspace
	h.call(t, "GET", "/api/workspaces/app", nil, &w)
	if w.Folder != "/workspaces/app" {
		t.Fatalf("folder = %q, want the node's /workspaces/app", w.Folder)
	}
	var list []workspace.Workspace
	h.call(t, "GET", "/api/workspaces", nil, &list)
	if len(list) != 1 || list[0].Folder != "/workspaces/app" {
		t.Fatalf("list: %+v", list)
	}

	h.agent.mu.Lock()
	h.agent.down = true
	h.agent.mu.Unlock()
	w = workspace.Workspace{}
	if code := h.call(t, "GET", "/api/workspaces/app", nil, &w); code != 200 || w.Folder != "" || w.State != "running" {
		t.Fatalf("node down: %d %+v (want the last known row, no folder)", code, w)
	}
}

// A node whose heartbeats stopped isn't called when listing: waiting out an agent timeout per
// dead node made `GET /api/workspaces` (and `walroos ssh-config`) hang. Its rows show as last
// known; once the node is back, the live state shows again.
func TestOfflineNodeNotCalled(t *testing.T) {
	h := newHub(t, true)
	if code := h.call(t, "POST", "/api/workspaces", map[string]any{"name": "app", "repo_url": "https://github.com/me/app"}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	h.agent.set("app", agent.Workspace{Name: "app", State: "running"})
	var w workspace.Workspace
	h.call(t, "GET", "/api/workspaces/app", nil, &w) // stores "running"

	// The node goes quiet (no heartbeat for a minute) while its agent would now say "stopped".
	h.agent.set("app", agent.Workspace{Name: "app", State: "stopped"})
	h.db.Write.Exec(`UPDATE nodes SET last_seen = ?`, time.Now().UTC().Add(-time.Minute))
	var list []workspace.Workspace
	h.call(t, "GET", "/api/workspaces", nil, &list)
	h.call(t, "GET", "/api/workspaces/app", nil, &w)
	if len(list) != 1 || list[0].State != "running" || w.State != "running" {
		t.Fatalf("offline node was asked: list %+v, get %+v (want the stored running)", list, w)
	}

	h.db.Write.Exec(`UPDATE nodes SET last_seen = ?`, time.Now().UTC())
	h.call(t, "GET", "/api/workspaces/app", nil, &w)
	if w.State != "stopped" {
		t.Fatalf("back online: %q, want the live stopped", w.State)
	}
}
