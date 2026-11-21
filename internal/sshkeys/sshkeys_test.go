package sshkeys_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/sshkeys"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"golang.org/x/crypto/ssh"
)

const owner, member = "owner@github", "member@github"

func TestKeysAndAgentAuthorization(t *testing.T) {
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
	users.EnsureOwner(ctx, owner)
	if _, err := db.Write.Exec(`INSERT INTO users (login, role, created_at) VALUES (?, 'developer', ?)`, member, time.Now()); err != nil {
		t.Fatal(err)
	}

	fleetSvc := fleet.NewService(fleet.NewStore(db))
	h := sshkeys.NewHandler(sshkeys.NewStore(db), fleetSvc.Authenticate)
	caller := owner
	r := whttp.NewRouter()
	h.NodeRoutes(r)
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(func(*http.Request) (string, error) { return caller, nil }, users))
		h.UserRoutes(r)
	})

	call := func(method, path, token string, body, out any) int {
		t.Helper()
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		req := httptest.NewRequest(method, path, &buf)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if out != nil && rec.Code < 300 {
			json.NewDecoder(rec.Body).Decode(out)
		}
		return rec.Code
	}

	ownerKey, ownerFP := newKey(t, "laptop")
	var added sshkeys.Key
	if code := call("POST", "/api/ssh-keys", "", map[string]string{"public_key": ownerKey}, &added); code != 201 {
		t.Fatalf("add key: %d", code)
	}
	if added.Name != "laptop" || added.Fingerprint != ownerFP || strings.HasSuffix(added.PublicKey, "laptop") {
		t.Fatalf("added = %+v, want name from the comment and the key without it", added)
	}
	if code := call("POST", "/api/ssh-keys", "", map[string]string{"name": "again", "public_key": ownerKey}, nil); code != 409 {
		t.Errorf("same key twice: %d, want 409", code)
	}
	for _, bad := range []string{"", "ssh-ed25519 notbase64", ownerKey + "\n" + ownerKey} {
		if code := call("POST", "/api/ssh-keys", "", map[string]string{"name": "x", "public_key": bad}, nil); code != 400 {
			t.Errorf("invalid key %q: %d, want 400", bad, code)
		}
	}

	caller = member
	memberKey, memberFP := newKey(t, "")
	if code := call("POST", "/api/ssh-keys", "", map[string]string{"public_key": memberKey}, nil); code != 400 {
		t.Errorf("no name and no comment: %d, want 400", code)
	}
	if code := call("POST", "/api/ssh-keys", "", map[string]string{"name": "desktop", "public_key": memberKey}, nil); code != 201 {
		t.Fatalf("member add key: %d", code)
	}
	var list []sshkeys.Key
	if call("GET", "/api/ssh-keys", "", nil, &list); len(list) != 1 || list[0].Name != "desktop" {
		t.Fatalf("member sees %+v, want only their key", list)
	}
	if code := call("DELETE", "/api/ssh-keys/"+added.ID, "", nil, nil); code != 404 {
		t.Errorf("member deletes owner's key: %d, want 404", code)
	}

	// A node asks about keys with its own token.
	joinToken, _, _ := fleetSvc.CreateJoinToken(ctx)
	node, nodeToken, err := fleetSvc.Join(ctx, joinToken, "homelab", []string{"dev"})
	if err != nil {
		t.Fatal(err)
	}
	authz := func(token, ws, fp string) (int, string) {
		var out struct{ Login string }
		code := call("POST", "/api/nodes/"+node.ID+"/ssh-auth", token, map[string]string{"workspace": ws, "fingerprint": fp}, &out)
		return code, out.Login
	}
	if code, _ := authz("wlr_node_wrong", "app", ownerFP); code != 401 {
		t.Errorf("wrong node token: %d, want 401", code)
	}
	if code, login := authz(nodeToken, "app", ownerFP); code != 200 || login != owner {
		t.Errorf("owner key: %d %q, want 200 %s", code, login, owner)
	}
	if code, _ := authz(nodeToken, "app", memberFP); code != 403 {
		t.Errorf("member key on a workspace that isn't theirs: %d, want 403", code)
	}
	if _, err := db.Write.Exec(`INSERT INTO workspaces (id, name, owner, node_id, repo_url, state, created_at) VALUES ('w1', 'app', ?, ?, 'x', 'running', ?)`,
		member, node.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, login := authz(nodeToken, "app", memberFP); code != 200 || login != member {
		t.Errorf("member key on their workspace: %d %q", code, login)
	}
	if code, _ := authz(nodeToken, "app", "SHA256:unknown"); code != 403 {
		t.Errorf("unknown key: %d, want 403", code)
	}

	caller = owner
	if code := call("DELETE", "/api/ssh-keys/"+added.ID, "", nil, nil); code != 204 {
		t.Fatalf("delete own key: %d", code)
	}
	if code, _ := authz(nodeToken, "app", ownerFP); code != 403 {
		t.Errorf("deleted key: %d, want 403", code)
	}
}

func newKey(t *testing.T, comment string) (line, fingerprint string) {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment, ssh.FingerprintSHA256(sshPub)
}
