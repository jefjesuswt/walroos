package hooks_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/hooks"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

type hub struct {
	http http.Handler
	db   *config.Database
	apps *app.Service
}

// newHub has two apps on one prod node: web (a repo on branch main) and site (a published
// image). Nothing runs the deploy queue: a hook's work ends at "queued".
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
	for _, q := range []string{
		`INSERT INTO nodes (id, name, roles, token_hash, address, created_at, last_seen) VALUES ('n1', 'prod-1', 'prod', 'h', '100.64.0.1', ?, ?)`,
		`INSERT INTO apps (id, name, node_id, repo_url, branch, port, created_at) VALUES ('a1', 'web', 'n1', 'https://github.com/me/web', 'main', 3000, ?)`,
		`INSERT INTO apps (id, name, node_id, repo_url, image, port, created_at) VALUES ('a2', 'site', 'n1', '', 'ghcr.io/me/site:latest', 80, ?)`,
	} {
		args := []any{now}
		if strings.Count(q, "?") == 2 {
			args = append(args, now)
		}
		if _, err := db.Write.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	refuse := func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("no agents in this test")
	}
	agents := &agent.Client{HTTP: &http.Client{Transport: &http.Transport{DialContext: refuse}, Timeout: time.Second}}
	apps := app.NewService(app.NewStore(db), fleet.NewService(fleet.NewStore(db)), agents, box, settings.NewService(db, box))
	h := hooks.NewHandler(apps)
	r := whttp.NewRouter()
	h.HookRoutes(r) // no user auth: the secret or the token is the auth
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		app.NewHandler(apps).Routes(r)
		h.UserRoutes(r)
	})
	return &hub{http: r, db: db, apps: apps}
}

func (h *hub) do(t *testing.T, method, path string, header map[string]string, body []byte, out any) int {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.http.ServeHTTP(rec, req)
	if out != nil {
		json.Unmarshal(rec.Body.Bytes(), out)
	}
	return rec.Code
}

func (h *hub) deployments(t *testing.T, app string) int {
	t.Helper()
	var n int
	h.db.Read.QueryRow(`SELECT count(*) FROM deployments d JOIN apps a ON a.id = d.app_id WHERE a.name = ?`, app).Scan(&n)
	return n
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func pushBody(ref, after string) []byte {
	b, _ := json.Marshal(map[string]any{"ref": ref, "after": after,
		"repository": map[string]string{"default_branch": "main"}, "project": map[string]string{"default_branch": "main"}})
	return b
}

func TestPushHooks(t *testing.T) {
	h := newHub(t)
	ctx := context.Background()
	body := pushBody("refs/heads/main", sha)

	// manual: a valid signature still deploys nothing
	secret, _ := h.apps.WebhookSecret(ctx, "web")
	if code := h.do(t, "POST", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, body)}, body, nil); code != 401 {
		t.Errorf("push to a manual app: %d, want 401", code)
	}
	var a app.App
	if code := h.do(t, "PUT", "/api/apps/web/trigger", nil, []byte(`{"trigger":"on_push"}`), &a); code != 200 || a.Trigger != app.OnPush {
		t.Fatalf("set trigger: %d %+v", code, a)
	}
	var got struct{ Secret string }
	if h.do(t, "GET", "/api/apps/web/webhook-secret", nil, nil, &got); got.Secret != secret {
		t.Fatalf("the secret the UI shows changed: %q, want %q", got.Secret, secret)
	}

	type ans struct {
		Deployed bool
		Reason   string
		Sha      string
	}
	for _, c := range []struct {
		name   string
		path   string
		header map[string]string
		body   []byte
		code   int
		deploy bool
	}{
		{"github push", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, body)}, body, 202, true},
		{"gitea push", "/hooks/gitea/web", map[string]string{"X-Gitea-Event": "push", "X-Gitea-Signature": sign(secret, body)}, body, 202, true},
		{"gitlab push", "/hooks/gitlab/web", map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": secret}, body, 202, true},
		{"github ping", "/hooks/github/web", map[string]string{"X-GitHub-Event": "ping", "X-Hub-Signature-256": "sha256=" + sign(secret, []byte(`{}`))}, []byte(`{}`), 200, false},
		{"another branch", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, pushBody("refs/heads/dev", sha))}, pushBody("refs/heads/dev", sha), 200, false},
		{"a tag", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, pushBody("refs/tags/v1", sha))}, pushBody("refs/tags/v1", sha), 200, false},
		{"branch deleted", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, pushBody("refs/heads/main", strings.Repeat("0", 40)))}, pushBody("refs/heads/main", strings.Repeat("0", 40)), 200, false},
		{"wrong signature", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign("nope", body)}, body, 401, false},
		{"no signature", "/hooks/gitea/web", map[string]string{"X-Gitea-Event": "push"}, body, 401, false},
		{"wrong gitlab token", "/hooks/gitlab/web", map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "nope"}, body, 401, false},
		{"unknown app", "/hooks/github/nope", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, body)}, body, 401, false},
	} {
		before := h.deployments(t, "web")
		var out ans
		code := h.do(t, "POST", c.path, c.header, c.body, &out)
		if code != c.code || out.Deployed != c.deploy || (h.deployments(t, "web") > before) != c.deploy {
			t.Errorf("%s: %d %+v, want %d deployed=%v", c.name, code, out, c.code, c.deploy)
		}
		if c.deploy && out.Sha != sha {
			t.Errorf("%s: deployed %q, want the pushed commit", c.name, out.Sha)
		}
	}

	// A new secret: the old one stops working.
	var rotated struct{ Secret string }
	h.do(t, "POST", "/api/apps/web/webhook-secret", nil, nil, &rotated)
	if rotated.Secret == secret {
		t.Fatal("rotating kept the secret")
	}
	if code := h.do(t, "POST", "/hooks/github/web", map[string]string{"X-GitHub-Event": "push", "X-Hub-Signature-256": "sha256=" + sign(secret, body)}, body, nil); code != 401 {
		t.Errorf("old secret after rotating: %d", code)
	}
}

func TestDeployTokenHook(t *testing.T) {
	h := newHub(t)
	deploy := func(app, token string, body []byte) int {
		return h.do(t, "POST", "/hooks/deploy/"+app, map[string]string{"Authorization": "Bearer " + token}, body, nil)
	}
	if code := deploy("web", "wlr_deploy_x", nil); code != 401 {
		t.Errorf("no token made yet: %d", code)
	}
	var tok struct{ Token string }
	if code := h.do(t, "POST", "/api/apps/web/deploy-token", nil, nil, &tok); code != 201 || !strings.HasPrefix(tok.Token, "wlr_deploy_") {
		t.Fatalf("new token: %d %+v", code, tok)
	}
	var a app.App
	if h.do(t, "GET", "/api/apps/web", nil, nil, &a); !a.DeployToken {
		t.Error("the app doesn't say it has a deploy token")
	}
	for _, c := range []struct {
		name, app, token string
		body             []byte
		code             int
	}{
		{"with a commit", "web", tok.Token, []byte(`{"sha":"` + sha + `"}`), 202},
		{"branch head", "web", tok.Token, nil, 202},
		{"bad commit", "web", tok.Token, []byte(`{"sha":"not-hex"}`), 400},
		{"bad JSON", "web", tok.Token, []byte(`sha=1`), 400},
		{"wrong token", "web", "wlr_deploy_nope", nil, 401},
		{"another app's token", "site", tok.Token, nil, 401},
	} {
		if code := deploy(c.app, c.token, c.body); code != c.code {
			t.Errorf("%s: %d, want %d", c.name, code, c.code)
		}
	}
	if n := h.deployments(t, "web"); n != 2 {
		t.Errorf("web has %d deployments, want 2", n)
	}

	// An image app ignores the commit CI sends: it pulls its tag again.
	var site struct{ Token string }
	h.do(t, "POST", "/api/apps/site/deploy-token", nil, nil, &site)
	if code := deploy("site", site.Token, []byte(`{"sha":"`+sha+`"}`)); code != 202 {
		t.Errorf("image app with a commit: %d", code)
	}

	// A new token replaces the old one; revoking leaves none.
	var again struct{ Token string }
	h.do(t, "POST", "/api/apps/web/deploy-token", nil, nil, &again)
	if code := deploy("web", tok.Token, nil); code != 401 {
		t.Errorf("old token after a new one: %d", code)
	}
	h.do(t, "DELETE", "/api/apps/web/deploy-token", nil, nil, nil)
	if code := deploy("web", again.Token, nil); code != 401 {
		t.Errorf("revoked token: %d", code)
	}
}

func TestHooksStatus(t *testing.T) {
	h := newHub(t)
	var st hooks.Status
	if h.do(t, "GET", "/api/hooks", nil, nil, &st); st.URL != "" {
		t.Errorf("before the listener is up: %+v", st)
	}
}
