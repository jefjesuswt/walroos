package whttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowHosts(t *testing.T) {
	h := AllowHosts(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), "walroos-hub", "walroos-hub.tail1.ts.net")
	for host, want := range map[string]int{
		"walroos-hub":                     200,
		"WALROOS-HUB:80":                  200,
		"walroos-hub.tail1.ts.net.":       200,
		"100.64.0.1":                    200,
		"100.64.0.1:80":                 200,
		"[fd7a:115c::1]:80":               200,
		"evil.example":                    421, // DNS rebinding
		"walroos-hub.evil.example":        421,
		"walroos-hub.tail1.ts.net.evil.x": 421,
		"":                                421,
	} {
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %q: %d, want %d", host, rec.Code, want)
		}
	}
}
