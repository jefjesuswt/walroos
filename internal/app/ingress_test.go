package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/cloudflare"
)

// fakeCF is Cloudflare's API as walroos uses it: one zone, tunnels, their ingress and DNS.
type fakeCF struct {
	mu      sync.Mutex
	created int
	tunnels map[string]string // name → id
	ingress []cloudflare.Rule
	records map[string]cloudflare.Record // name → record
}

func newFakeCF(t *testing.T) (*fakeCF, string) {
	f := &fakeCF{tunnels: map[string]string{}, records: map[string]cloudflare.Record{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakeCF) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(result any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}
	auth := r.Header.Get("Authorization")
	if auth == "Bearer zones-only" && r.URL.Path == "/zones" {
		auth = "Bearer good" // can list zones, nothing else
	}
	if auth != "Bearer good" {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 9109, "message": "Invalid access token"}}})
		return
	}
	p := r.URL.Path
	switch {
	case p == "/zones":
		ok([]map[string]any{{"id": "z1", "name": "example.com", "account": map[string]string{"id": "acc1"}}})
	case p == "/accounts/acc1/cfd_tunnel" && r.Method == "GET":
		if id, found := f.tunnels[r.URL.Query().Get("name")]; found {
			ok([]map[string]string{{"id": id}})
		} else {
			ok([]any{})
		}
	case p == "/accounts/acc1/cfd_tunnel" && r.Method == "POST":
		var in struct{ Name string }
		json.NewDecoder(r.Body).Decode(&in)
		f.created++
		f.tunnels[in.Name] = fmt.Sprintf("tun%d", f.created)
		ok(map[string]string{"id": f.tunnels[in.Name]})
	case strings.HasSuffix(p, "/token"):
		ok("eyJhIjoiYWNjMSIsInQiOiJ0dW4xIn0=")
	case strings.HasSuffix(p, "/configurations"):
		var in struct {
			Config struct{ Ingress []cloudflare.Rule }
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.ingress = in.Config.Ingress
		ok(nil)
	case p == "/zones/z1/dns_records" && r.Method == "GET":
		recs := []cloudflare.Record{}
		if rec, found := f.records[r.URL.Query().Get("name")]; found {
			recs = append(recs, rec)
		}
		ok(recs)
	case strings.HasPrefix(p, "/zones/z1/dns_records"):
		var rec cloudflare.Record
		json.NewDecoder(r.Body).Decode(&rec)
		id := strings.TrimPrefix(strings.TrimPrefix(p, "/zones/z1/dns_records"), "/")
		for name, old := range f.records {
			if old.ID == id && id != "" {
				delete(f.records, name)
			}
		}
		if r.Method != "DELETE" {
			rec.ID = "r-" + rec.Name
			f.records[rec.Name] = rec
		}
		ok(nil)
	default:
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 7003, "message": "no route " + p}}})
	}
}

func TestAppDomains(t *testing.T) {
	h := newHub(t, "prod")
	cf, url := newFakeCF(t)
	h.svc.CloudflareAPI = url
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "web", RepoURL: "https://example.com/web.git", Port: 8000}, nil)

	var st app.CloudflareStatus
	if h.call(t, "GET", "/api/settings/cloudflare", nil, &st); st.Configured {
		t.Fatalf("configured before any token: %+v", st)
	}
	if code := h.call(t, "PUT", "/api/apps/web/domains", []string{"web.example.com"}, nil); code != 409 {
		t.Errorf("domains without Cloudflare: %d", code)
	}
	if code := h.call(t, "PUT", "/api/settings/cloudflare", map[string]string{"token": "bad"}, nil); code == 200 {
		t.Error("a token Cloudflare rejects was stored")
	}
	// A token that passes the zone check but can't make tunnels says so, not a bare 500.
	h.call(t, "PUT", "/api/settings/cloudflare", map[string]string{"token": "zones-only"}, nil)
	var e struct{ Error string }
	if code := h.call(t, "PUT", "/api/apps/web/domains", []string{"web.example.com"}, &e); code != 502 || !strings.Contains(e.Error, "lacks a permission") {
		t.Errorf("domains with a zones-only token: %d %q", code, e.Error)
	}
	if code := h.call(t, "PUT", "/api/settings/cloudflare", map[string]string{"token": "good"}, &st); code != 200 || !st.Configured || st.Zones[0] != "example.com" {
		t.Fatalf("set token: %d %+v", code, st)
	}
	var sealed string
	h.db.Read.QueryRow(`SELECT value FROM settings WHERE key = 'cloudflare_token'`).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, "good") {
		t.Errorf("token stored as %q", sealed)
	}

	var a app.App
	if code := h.call(t, "PUT", "/api/apps/web/domains", []string{"Web.Example.com.", "web.example.com"}, &a); code != 200 || strings.Join(a.Domains, ",") != "web.example.com" {
		t.Fatalf("set domains: %d %+v", code, a)
	}
	if cf.tunnels["walroos-prod-1"] != "tun1" || h.agent.tunnel == "" {
		t.Errorf("tunnel %v, agent token %q", cf.tunnels, h.agent.tunnel)
	}
	if got := fmt.Sprint(cf.ingress); got != "[{web.example.com http://web:8000} { http_status:404}]" {
		t.Errorf("ingress = %s", got)
	}
	if rec := cf.records["web.example.com"]; rec.Type != "CNAME" || rec.Content != "tun1.cfargotunnel.com" {
		t.Errorf("record = %+v", rec)
	}

	for _, bad := range [][]string{{"bad domain"}, {"-x.example.com"}, {"x.other.org"}, {"a.example.com\nb"}} {
		if code := h.call(t, "PUT", "/api/apps/web/domains", bad, nil); code != 400 {
			t.Errorf("domains %q: %d", bad, code)
		}
	}
	h.call(t, "POST", "/api/apps", app.CreateInput{Name: "api", RepoURL: "https://example.com/api.git", Port: 3000}, nil)
	if code := h.call(t, "PUT", "/api/apps/api/domains", []string{"web.example.com"}, nil); code != 409 {
		t.Errorf("a domain of another app: %d", code)
	}

	// Someone else's record is never overwritten, and the domains stay as they were.
	cf.records["blog.example.com"] = cloudflare.Record{ID: "r0", Type: "A", Name: "blog.example.com", Content: "1.2.3.4"}
	if code := h.call(t, "PUT", "/api/apps/api/domains", []string{"blog.example.com"}, nil); code == 200 {
		t.Error("took over an A record")
	}
	if h.call(t, "GET", "/api/apps/api", nil, &a); len(a.Domains) != 0 || cf.records["blog.example.com"].Content != "1.2.3.4" {
		t.Errorf("after the failed takeover: %v, %+v", a.Domains, cf.records["blog.example.com"])
	}

	// Moving web to another name drops the old CNAME; the tunnel is reused.
	h.call(t, "PUT", "/api/apps/web/domains", []string{"www.example.com"}, nil)
	if _, left := cf.records["web.example.com"]; left || cf.records["www.example.com"].Content != "tun1.cfargotunnel.com" || cf.created != 1 {
		t.Errorf("records %v, tunnels created %d", cf.records, cf.created)
	}

	if code := h.call(t, "DELETE", "/api/apps/web", nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, left := cf.records["www.example.com"]; left || fmt.Sprint(cf.ingress) != "[{ http_status:404}]" {
		t.Errorf("after delete: records %v, ingress %v", cf.records, cf.ingress)
	}
}
