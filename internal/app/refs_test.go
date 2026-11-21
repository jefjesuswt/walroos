package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/shared/envref"
)

// References in an app's variables are checked when saved and expanded on each deploy, with
// what the project has at that moment.
func TestEnvReferences(t *testing.T) {
	h := newHub(t, "prod")
	db := map[string]string{"URL": "postgres://u:p@walroos-db-shop-db:5432/shop", "HOST": "walroos-db-shop-db"}
	h.svc.SetResolver(func(_ context.Context, projectID string) (envref.Vars, error) {
		if projectID != "default" {
			t.Errorf("resolved project %q", projectID)
		}
		return envref.Vars{"shop-db": db, "shared": {"STRIPE_KEY": "sk_test"}}, nil
	})
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://example.com/web.git", Port: 8000}, nil)

	env := []map[string]any{
		{"key": "DATABASE_URL", "value": "${{shop-db.URL}}"},
		{"key": "STRIPE", "value": "key=${{ shared.STRIPE_KEY }}"},
		{"key": "PLAIN", "value": "$HOME stays"},
	}
	if code := h.call(t, "PUT", "/api/apps/web/env", env, nil); code != 200 {
		t.Fatalf("env with references: %d", code)
	}
	var stored []map[string]any
	if h.call(t, "GET", "/api/apps/web/env", nil, &stored); stored[0]["value"] != "${{shop-db.URL}}" {
		t.Errorf("the app keeps the reference, not the value: %v", stored)
	}
	for _, bad := range []string{"${{api.URL}}", "${{shop-db.NOPE}}"} {
		var e struct{ Error string }
		if code := h.call(t, "PUT", "/api/apps/web/env", []map[string]any{{"key": "X", "value": bad}}, &e); code != 400 || !strings.Contains(e.Error, "X: "+bad) {
			t.Errorf("saving %s: %d %q", bad, code, e.Error)
		}
	}

	var d app.Deployment
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	if got := h.wait(t, "web", d.ID); got.State != "succeeded" {
		t.Fatalf("deploy: %+v", got)
	}
	want := []string{"DATABASE_URL=" + db["URL"], "PLAIN=$HOME stays", "STRIPE=key=sk_test"}
	if got := h.agent.specs[0].Env; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("container env = %q, want %q", got, want)
	}

	// The database moved: the next deploy follows it.
	db["URL"] = "postgres://u:p@elsewhere:5432/shop"
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	h.wait(t, "web", d.ID)
	if got := h.agent.specs[1].Env[0]; got != "DATABASE_URL=postgres://u:p@elsewhere:5432/shop" {
		t.Errorf("after the database changed: %q", got)
	}

	// It's gone: the deploy fails and says which variable.
	delete(db, "URL")
	h.call(t, "POST", "/api/apps/web/deploy", map[string]string{}, &d)
	if got := h.wait(t, "web", d.ID); got.State != "failed" || !strings.Contains(got.Error, "variable DATABASE_URL: ${{shop-db.URL}}") {
		t.Errorf("deploy with a dangling reference: %+v", got)
	}
}
