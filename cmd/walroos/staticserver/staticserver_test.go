package staticserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	site := fstest.MapFS{
		"index.html":           {Data: []byte("home")},
		"assets/app-1a2b.js":   {Data: []byte("js")},
		"docs/index.html":      {Data: []byte("docs")},
		"empty/x.txt":          {Data: []byte("x")},
		"404.html":             {Data: []byte("missing")},
		".git/config":          {Data: []byte("secret")},
		".env":                 {Data: []byte("secret")},
		".well-known/security": {Data: []byte("contact")},
	}
	cases := []struct {
		spa         bool
		method, url string
		status      int
		body        string
	}{
		{false, "GET", "/", 200, "home"},
		{false, "GET", "/assets/app-1a2b.js", 200, "js"},
		{false, "GET", "/docs/", 200, "docs"},
		{false, "GET", "/about", 404, "missing"},
		{true, "GET", "/about/team", 200, "home"},
		{true, "GET", "/assets/gone.js", 404, "missing"}, // a missing asset is a 404, not the app
		{true, "GET", "/empty/", 200, "home"},            // folders are never listed
		{true, "GET", "/.git/config", 404, "missing"},
		{false, "GET", "/.env", 404, "missing"},
		{false, "GET", "/a/../.env", 404, "missing"},
		{false, "GET", "/.well-known/security", 200, "contact"},
		{false, "POST", "/", 405, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		Handler(site, c.spa).ServeHTTP(rec, httptest.NewRequest(c.method, c.url, nil))
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("spa=%v %s %s = %d %q, want %d %q", c.spa, c.method, c.url, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
	rec := httptest.NewRecorder()
	Handler(site, false).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("pages should be no-cache, got %q", rec.Header().Get("Cache-Control"))
	}
	if delete(site, "404.html"); true {
		rec := httptest.NewRecorder()
		Handler(site, false).ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("no 404.html: got %d", rec.Code)
		}
	}
}
