package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// The token flows against GitHub and Cloudflare are in internal/workspace (TestGitHubToken) and
// internal/app (TestCloudflare), with their fakes; this covers what's settings' own.
func TestSettings(t *testing.T) {
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
	box, _ := secretbox.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	svc := settings.NewService(db, box)
	r := whttp.NewRouter()
	r.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(auth.Static("owner@github"), users))
		settings.NewHandler(svc).Routes(r)
	})
	call := func(method, path string, body, out any) int {
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
		if out != nil {
			json.Unmarshal(rec.Body.Bytes(), out)
		}
		return rec.Code
	}

	var gh settings.GitHubStatus
	if code := call("GET", "/api/settings/github", nil, &gh); code != 200 || gh.Configured {
		t.Errorf("github before a token: %d %+v", code, gh)
	}
	var cf settings.CloudflareStatus
	if code := call("GET", "/api/settings/cloudflare", nil, &cf); code != 200 || cf.Configured || cf.Zones == nil {
		t.Errorf("cloudflare before a token: %d %+v (zones must be [], not null)", code, cf)
	}
	for _, path := range []string{"/api/settings/github", "/api/settings/cloudflare"} {
		if code := call("PUT", path, map[string]string{"token": "  "}, nil); code != 400 {
			t.Errorf("PUT %s with a blank token: %d, want 400", path, code)
		}
	}
	if _, err := svc.GitHubClient(ctx); err != settings.ErrNoGitHub {
		t.Errorf("GitHubClient without a token: %v", err)
	}
	if _, err := svc.CloudflareClient(ctx); err != settings.ErrNoCloudflare {
		t.Errorf("CloudflareClient without a token: %v", err)
	}
	if code := call("DELETE", "/api/settings/github", nil, nil); code != 204 {
		t.Errorf("DELETE github with nothing saved: %d", code)
	}
}
