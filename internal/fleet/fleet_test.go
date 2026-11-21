package fleet_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

const hubID = "nHubStableID"

// newHub wires the same routes as `walroos hub`, on a temp SQLite, with login as the caller.
func newHub(t *testing.T, login string) http.Handler {
	return newHubWithAgents(t, login, &agent.Client{HTTP: http.DefaultClient})
}

func newHubWithAgents(t *testing.T, login string, agents *agent.Client) http.Handler {
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
	if err := db.Migrate(ctx); err != nil { // idempotent
		t.Fatal("second migrate:", err)
	}

	users := auth.NewStore(db)
	if err := users.EnsureOwner(ctx, "owner@github"); err != nil {
		t.Fatal(err)
	}

	h := fleet.NewHandler(fleet.NewService(fleet.NewStore(db)), "http://walroos-hub", hubID, agents)
	r := whttp.NewRouter()
	h.NodeRoutes(r)
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static(login), users))
		r.Get("/api/me", auth.HandleMe)
		h.UserRoutes(r)
	})
	return r
}

func call(t *testing.T, h http.Handler, method, path, token string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = "100.101.12.4:41000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil && rec.Code < 300 {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return rec.Code
}

func TestJoinHeartbeatListRemove(t *testing.T) {
	hub := newHub(t, "owner@github")

	var jt struct{ Token, Command string }
	if code := call(t, hub, "POST", "/api/nodes/join-tokens", "", nil, &jt); code != 201 {
		t.Fatalf("create join token: %d", code)
	}
	if want := "walroos node join --hub http://walroos-hub --token " + jt.Token; jt.Command != want {
		t.Fatalf("command = %q, want %q", jt.Command, want)
	}

	// validation happens before the token is consumed
	if code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": "Bad_Name", "roles": []string{"dev"}}, nil); code != 400 {
		t.Fatalf("bad name: %d, want 400", code)
	}
	if code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": "homelab-1", "roles": []string{"dev", "gpu"}}, nil); code != 400 {
		t.Fatalf("bad role: %d, want 400", code)
	}

	var joined struct {
		Node  fleet.Node
		Token string
	}
	if code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": "homelab-1", "roles": []string{"build", "dev", "dev"}}, &joined); code != 201 {
		t.Fatalf("join: %d", code)
	}
	if got := joined.Node.Roles; len(got) != 2 || got[0] != "dev" || got[1] != "build" {
		t.Fatalf("roles = %v, want [dev build]", got)
	}
	if joined.Node.Status != "pending" {
		t.Fatalf("status before heartbeat = %q, want pending", joined.Node.Status)
	}

	if code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": "homelab-2", "roles": []string{"prod"}}, nil); code != 401 {
		t.Fatalf("reused join token: %d, want 401", code)
	}

	hb := "/api/nodes/" + joined.Node.ID + "/heartbeat"
	if code := call(t, hub, "POST", hb, "wlr_node_wrong", fleet.Telemetry{}, nil); code != 401 {
		t.Fatalf("wrong node token: %d, want 401", code)
	}
	var hbResp struct {
		HubID string `json:"hub_id"`
	}
	if code := call(t, hub, "POST", hb, joined.Token, fleet.Telemetry{CPUPercent: 23.5, Podman: "5.4.1", Containers: 4}, &hbResp); code != 200 || hbResp.HubID != hubID {
		t.Fatalf("heartbeat: %d, hub_id %q", code, hbResp.HubID)
	}

	var nodes []fleet.Node
	if code := call(t, hub, "GET", "/api/nodes", "", nil, &nodes); code != 200 {
		t.Fatalf("list: %d", code)
	}
	n := nodes[0]
	if len(nodes) != 1 || n.Status != "online" || n.Telemetry.CPUPercent != 23.5 || n.Telemetry.Podman != "5.4.1" || n.Address != "100.101.12.4" {
		t.Fatalf("listed node = %+v", n)
	}

	// Roles can change after joining; they're normalized like at join.
	var changed fleet.Node
	if code := call(t, hub, "PUT", "/api/nodes/"+n.ID+"/roles", "", map[string]any{"roles": []string{"prod", "dev", "prod"}}, &changed); code != 200 {
		t.Fatalf("set roles: %d", code)
	}
	if got := changed.Roles; len(got) != 2 || got[0] != "dev" || got[1] != "prod" || changed.Status != "online" {
		t.Fatalf("after set roles: %+v", changed)
	}
	for _, bad := range [][]string{{}, nil, {"gpu"}, {"dev", "gpu"}} {
		if code := call(t, hub, "PUT", "/api/nodes/"+n.ID+"/roles", "", map[string]any{"roles": bad}, nil); code != 400 {
			t.Errorf("roles %v: %d, want 400", bad, code)
		}
	}
	if code := call(t, hub, "PUT", "/api/nodes/nope/roles", "", map[string]any{"roles": []string{"dev"}}, nil); code != 404 {
		t.Errorf("roles of an unknown node: %d, want 404", code)
	}
	call(t, hub, "GET", "/api/nodes", "", nil, &nodes)
	if got := nodes[0].Roles; len(got) != 2 || got[1] != "prod" {
		t.Errorf("listed roles = %v", got)
	}

	if code := call(t, hub, "DELETE", "/api/nodes/"+n.ID, "", nil, nil); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code := call(t, hub, "POST", hb, joined.Token, fleet.Telemetry{}, nil); code != 401 {
		t.Fatalf("heartbeat after remove: %d, want 401 so the agent stops", code)
	}
	if code := call(t, hub, "DELETE", "/api/nodes/"+n.ID, "", nil, nil); code != 404 {
		t.Fatalf("remove twice: %d, want 404", code)
	}
}

func TestDuplicateNameKeepsJoinToken(t *testing.T) {
	hub := newHub(t, "owner@github")
	join := func(name string) int {
		var jt struct{ Token string }
		call(t, hub, "POST", "/api/nodes/join-tokens", "", nil, &jt)
		code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": name, "roles": []string{"dev"}}, nil)
		if code == 409 { // token must survive a failed join
			code = call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": name + "-b", "roles": []string{"dev"}}, nil)
		}
		return code
	}
	if join("homelab-1") != 201 || join("homelab-1") != 201 {
		t.Fatal("expected both joins to end in 201")
	}
}

func TestUserAuth(t *testing.T) {
	var me auth.User
	if code := call(t, newHub(t, "owner@github"), "GET", "/api/me", "", nil, &me); code != 200 || me.Role != "owner" {
		t.Fatalf("owner /api/me: %d %+v", code, me)
	}
	stranger := newHub(t, "stranger@github")
	if code := call(t, stranger, "GET", "/api/nodes", "", nil, nil); code != 403 {
		t.Fatalf("stranger list: %d, want 403", code)
	}
	if code := call(t, stranger, "POST", "/api/nodes/join-tokens", "", nil, nil); code != 403 {
		t.Fatalf("stranger join token: %d, want 403", code)
	}
}

// TestHubCallsAgent runs the real hub router against a real agent server: the agent pins the
// hub from the heartbeat response, then /api/nodes/{id}/agent goes through.
func TestHubCallsAgent(t *testing.T) {
	var api *agent.Server
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(agentSrv.Close)

	// every node IP dials the test agent, like tsnet would dial the node
	dialAgent := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, agentSrv.Listener.Addr().String())
	}
	hub := newHubWithAgents(t, "owner@github", &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: dialAgent}}})

	var jt struct{ Token string }
	call(t, hub, "POST", "/api/nodes/join-tokens", "", nil, &jt)
	var joined struct {
		Node  fleet.Node
		Token string
	}
	if code := call(t, hub, "POST", "/api/nodes/join", "", map[string]any{"token": jt.Token, "name": "homelab-1", "roles": []string{"dev"}}, &joined); code != 201 {
		t.Fatalf("join: %d", code)
	}
	info := "/api/nodes/" + joined.Node.ID + "/agent"

	api = agent.NewServer(func(context.Context, string) (string, error) { return hubID, nil }, joined.Token)
	if code := call(t, hub, "GET", info, "", nil, nil); code != 409 {
		t.Fatalf("agent info before any heartbeat: %d, want 409 (no address yet)", code)
	}

	var hb struct {
		HubID string `json:"hub_id"`
	}
	call(t, hub, "POST", "/api/nodes/"+joined.Node.ID+"/heartbeat", joined.Token, fleet.Telemetry{}, &hb)
	if code := call(t, hub, "GET", info, "", nil, nil); code != 502 {
		t.Fatalf("agent info before the agent knows the hub: %d, want 502", code)
	}

	api.SetHubID(hb.HubID)
	var got agent.Info
	if code := call(t, hub, "GET", info, "", nil, &got); code != 200 || got.Walroos == "" {
		t.Fatalf("agent info: %d %+v", code, got)
	}
	if code := call(t, hub, "GET", "/api/nodes/nope/agent", "", nil, nil); code != 404 {
		t.Fatalf("unknown node: %d, want 404", code)
	}
}
