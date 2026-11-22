package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jefjesuswt/walroos/cmd/walroos/hubconn"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// fakeHub answers like the hub, behind the same cross-origin protection and host check.
func fakeHub(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":"you@github","role":"owner"}`))
	})
	mux.HandleFunc("POST /api/apps/web/power", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "host="+r.Host)
	})
	srv := httptest.NewServer(whttp.AllowHosts(http.NewCrossOriginProtection().Handler(mux), "walroos-hub"))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandler(t *testing.T) {
	hub := fakeHub(t)
	local := "http://127.0.0.1:7480"
	h := Handler(hub.URL, local, http.DefaultTransport)
	do := func(method, host, origin, site string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, local+"/api/apps/web/power", strings.NewReader(`{"action":"restart"}`))
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	// The panel's own write: forwarded with the hub's Host, accepted.
	if rec := do("POST", "127.0.0.1:7480", local, "same-origin"); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "host=127.0.0.1:") {
		t.Fatalf("panel write: %d %s", rec.Code, rec.Body)
	}
	// Another site posting to the local port: the hub refuses it.
	if rec := do("POST", "127.0.0.1:7480", "https://evil.example", "cross-site"); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site write: %d, want 403", rec.Code)
	}
	// A DNS-rebinding page (its own Host on 127.0.0.1): refused before the hub.
	if rec := do("POST", "evil.example:7480", "http://evil.example:7480", "same-origin"); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("rebound host: %d, want 421", rec.Code)
	}
}

func TestWhoAmI(t *testing.T) {
	ctx := context.Background()
	hub := fakeHub(t)
	if login, err := whoAmI(hub.URL)(ctx, http.DefaultClient); err != nil || login != "you@github" {
		t.Fatalf("login %q, %v", login, err)
	}
	for name, h := range map[string]http.HandlerFunc{
		"not on the allowlist": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"not allowed"}`))
		},
		"not a hub": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) },
	} {
		srv := httptest.NewServer(h)
		_, err := whoAmI(srv.URL)(ctx, http.DefaultClient)
		srv.Close()
		if !hubconn.IsAnswer(err) {
			t.Errorf("%s: %v, want an answer from the hub", name, err)
		}
	}
}
