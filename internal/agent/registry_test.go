package agent

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryHandlerAuth(t *testing.T) {
	ln, err := net.Listen("tcp", registryAddr) // stands in for the registry container
	if err != nil {
		t.Skipf("%s busy: %v", registryAddr, err)
	}
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(500) // the token must not reach the registry
		}
	})}
	go backend.Serve(ln)
	defer backend.Close()

	srv := httptest.NewServer(NewServer(nil, "tok").RegistryHandler())
	defer srv.Close()
	good := "Bearer " + PullToken(TokenHash("tok"), "web")
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
		{"GET", "/v2/other/manifests/abc", "Bearer " + PullToken(TokenHash("tok"), ""), 401},
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
