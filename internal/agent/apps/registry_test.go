package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
)

func TestRegistryAuthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); r.URL.Path != "/v2/" || !ok || u != "walroos" || p != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	orig := registryAddr
	registryAddr = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(func() { registryAddr = orig })

	if !registryAuthOK(context.Background(), "right") {
		t.Error("working password reported as broken")
	}
	if registryAuthOK(context.Background(), "stale") {
		t.Error("stale password (registry recreated by someone else) reported as fine")
	}
	srv.Close()
	if registryAuthOK(context.Background(), "right") {
		t.Error("registry down reported as fine")
	}
}

func TestRegistryHandlerAuth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "walroos" || p != "pw" {
			w.WriteHeader(500) // the registry gets its own password, never the pull token
		}
	}))
	defer backend.Close()
	old := registryAddr
	registryAddr = backend.Listener.Addr().String()
	defer func() { registryAddr = old }()

	a := New(quadlet.New())
	srv := httptest.NewServer(a.RegistryHandler(agent.TokenHash("tok")))
	defer srv.Close()
	get := func(path, auth string) int {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/v2/", ""); code != 503 {
		t.Errorf("before any build: %d, want 503", code)
	}
	a.regPass = "pw"
	good := "Bearer " + agent.PullToken(agent.TokenHash("tok"), "web")
	for _, c := range []struct {
		method, path, auth string
		want               int
	}{
		{"GET", "/v2/", "", 200},
		{"GET", "/v2/walroos-app-web/manifests/abc", good, 200},
		{"HEAD", "/v2/walroos-app-web/blobs/sha256:00", good, 200},
		{"GET", "/v2/walroos-app-web/manifests/abc", "", 401},
		{"GET", "/v2/walroos-app-api/manifests/abc", good, 401},                       // another app
		{"GET", "/v2/walroos-app-web/tags/list", good, 401},                           // not a pull
		{"GET", "/v2/walroos-app-web/manifests/..%2F..%2Fwalroos-app-api", good, 401}, // encoded path
		{"GET", "/v2/walroos-app-web/../walroos-app-api/manifests/x", good, 401},
		{"PUT", "/v2/walroos-app-web/manifests/abc", good, 405},
		{"GET", "/v2/other/manifests/abc", "Bearer " + agent.PullToken(agent.TokenHash("tok"), ""), 401},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		req.Header.Set("Authorization", c.auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
}
