package sshconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	list := []workspace{
		{Name: "web", NodeName: "homelab-1", State: "running"},
		{Name: "api", NodeName: "homelab-2", State: "stopped"},
	}

	plain := Format(list, "", 2222, ".walroos")
	want := `# walroos workspaces — append to ~/.ssh/config

Host api.walroos
  HostName homelab-2
  Port 2222
  User api

Host web.walroos
  HostName homelab-1
  Port 2222
  User web
`
	if plain != want {
		t.Fatalf("plain config:\n%s\nwant:\n%s", plain, want)
	}

	for _, proxy := range []string{"localhost:1055", "http://localhost:1055", "socks5://localhost:1055/"} {
		got := Format(list[:1], proxy, 2222, ".walroos")
		if !strings.Contains(got, "  ProxyCommand nc -X 5 -x localhost:1055 %h %p\n") {
			t.Errorf("proxy %q: missing or malformed ProxyCommand:\n%s", proxy, got)
		}
	}

	// Port and suffix are configurable, and a workspace without a node is skipped.
	custom := Format([]workspace{{Name: "x", NodeName: "n"}, {Name: "bad"}}, "", 2200, ".ws")
	if !strings.Contains(custom, "Host x.ws\n  HostName n\n  Port 2200\n  User x\n") {
		t.Fatalf("custom port/suffix:\n%s", custom)
	}
	if strings.Contains(custom, "Host bad") {
		t.Fatalf("a workspace without a node must be skipped:\n%s", custom)
	}
}

// TestGenerateAgainstHub runs the command's HTTP path against a real router-shaped server.
func TestGenerateAgainstHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "app", "node_name": "homelab-1", "state": "running"},
		})
	}))
	t.Cleanup(srv.Close)

	out, err := Generate(context.Background(), srv.URL, "", 2222, ".walroos")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Host app.walroos\n  HostName homelab-1\n") {
		t.Fatalf("config:\n%s", out)
	}

	// A hub that doesn't know you says so, instead of printing an empty config.
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"stranger@github is not allowed on this hub"}`))
	}))
	t.Cleanup(denied.Close)
	if _, err := Generate(context.Background(), denied.URL, "", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "doesn't know you") {
		t.Fatalf("403: %v", err)
	}

	// Unreachable hub: the error points at --proxy.
	if _, err := Generate(context.Background(), "http://127.0.0.1:1", "", 2222, ".walroos"); err == nil || !strings.Contains(err.Error(), "--proxy") {
		t.Fatalf("unreachable: %v", err)
	}
}
