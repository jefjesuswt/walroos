package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newHub serves /mcp like `walroos hub` does: real SQLite, real router and auth, a dev node
// whose agent is an in-memory fake. login is who the requests come from.
func newHub(t *testing.T, login string) (url string, created *agent.WorkspaceSpec) {
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
	now := time.Now().UTC()
	if _, err := db.Write.Exec(`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen)
		VALUES ('n1', 'homelab-1', 'dev', 'h', '100.64.0.7', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	created = &agent.WorkspaceSpec{}
	states := map[string]string{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		name, op, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/workspaces/"), "/")
		switch {
		case r.URL.Path == "/v1/detect":
			w.Write([]byte(`{"stack":"go","branch":"main","devcontainer":{"image":"mcr.microsoft.com/devcontainers/go:1"}}`))
		case r.Method == "POST" && r.URL.Path == "/v1/workspaces":
			json.NewDecoder(r.Body).Decode(created)
			states[created.Name] = "creating"
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(agent.Workspace{Name: created.Name, State: "creating"})
		case r.Method == "GET" && r.URL.Path == "/v1/workspaces":
			list := []agent.Workspace{}
			for n, s := range states {
				list = append(list, agent.Workspace{Name: n, State: s})
			}
			json.NewEncoder(w).Encode(list)
		case states[name] == "":
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"workspace not found"}`))
		default:
			if op == "start" {
				states[name] = "running"
			} else if op == "stop" {
				states[name] = "stopped"
			}
			json.NewEncoder(w).Encode(agent.Workspace{Name: name, State: states[name]})
		}
	}))
	t.Cleanup(fake.Close)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fake.Listener.Addr().String())
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dial}}}

	box, err := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := fleet.NewService(fleet.NewStore(db))
	ws := workspace.NewService(workspace.NewStore(db), nodes, agents, box)

	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static(login), users))
		r.Post("/mcp", Handler(nodes, ws).ServeHTTP)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp", created
}

func connect(t *testing.T, url string) *sdk.ClientSession {
	t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// call runs a tool and returns its structured output; isErr is the tool-level error flag.
func call(t *testing.T, s *sdk.ClientSession, tool string, args map[string]any) (out map[string]any, text string, isErr bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*sdk.TextContent); ok {
			text = tc.Text
		}
	}
	b, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(b, &out)
	return out, text, res.IsError
}

func TestMCPWorkspaceFlow(t *testing.T) {
	url, created := newHub(t, "owner@github")
	s := connect(t, url)

	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	for _, want := range []string{"list_nodes", "list_workspaces", "detect_stack", "create_workspace", "get_workspace", "start_workspace", "stop_workspace", "workspace_ssh_info"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing tool %s in %v", want, names)
		}
	}

	if out, _, _ := call(t, s, "list_nodes", nil); !strings.Contains(mustJSON(out), `"name":"homelab-1"`) {
		t.Fatalf("list_nodes: %v", out)
	}

	d, _, _ := call(t, s, "detect_stack", map[string]any{"repo_url": "https://github.com/a/b"})
	if d["stack"] != "go" {
		t.Fatalf("detect_stack: %v", d)
	}

	// The proposed devcontainer goes straight back into create_workspace.
	w, text, isErr := call(t, s, "create_workspace", map[string]any{"name": "app", "repo_url": "https://github.com/a/b", "devcontainer": d["devcontainer"]})
	if isErr || w["state"] != "creating" || w["owner"] != "owner@github" || w["node_name"] != "homelab-1" {
		t.Fatalf("create_workspace: %v %s", w, text)
	}
	if !strings.Contains(string(created.Devcontainer), "devcontainers/go") {
		t.Fatalf("devcontainer not passed to the agent: %s", created.Devcontainer)
	}

	if w, _, _ := call(t, s, "start_workspace", map[string]any{"name": "app"}); w["state"] != "running" {
		t.Fatalf("start: %v", w)
	}
	if w, _, _ := call(t, s, "stop_workspace", map[string]any{"name": "app"}); w["state"] != "stopped" {
		t.Fatalf("stop: %v", w)
	}
	if out, _, _ := call(t, s, "list_workspaces", nil); !strings.Contains(mustJSON(out), `"name":"app"`) {
		t.Fatalf("list_workspaces: %v", out)
	}

	info, _, _ := call(t, s, "workspace_ssh_info", map[string]any{"name": "app"})
	if info["command"] != "ssh -p 2222 app@100.64.0.7" || !strings.Contains(info["ssh_config"].(string), "Host app.walroos\n  HostName 100.64.0.7\n") {
		t.Fatalf("ssh info: %v", info)
	}

	// Service errors come back as tool errors the model can read, not protocol failures.
	if _, text, isErr := call(t, s, "get_workspace", map[string]any{"name": "nope"}); !isErr || !strings.Contains(text, "not found") {
		t.Fatalf("missing workspace: isErr=%v %q", isErr, text)
	}
	if _, text, isErr := call(t, s, "create_workspace", map[string]any{"name": "Bad Name", "repo_url": "x"}); !isErr || !strings.Contains(text, "DNS label") {
		t.Fatalf("invalid name: isErr=%v %q", isErr, text)
	}
}

func TestMCPRequiresAllowedUser(t *testing.T) {
	url, _ := newHub(t, "stranger@github")
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	if _, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url}, nil); err == nil {
		t.Fatal("a user outside the allowlist connected")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
