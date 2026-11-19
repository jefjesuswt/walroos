package agent

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// A build node keeps the images it builds in a registry (distribution) bound to localhost.
// The tailnet reaches it only through RegistryHandler: read-only, one app per token.
const (
	RegistryPort  = "5000" // on the tailnet
	registryName  = "walroos-registry"
	registryImage = "docker.io/library/registry:2"
)

var registryAddr = "127.0.0.1:7075" // tests change it

// PullToken lets a prod node pull app's images from a build node. It's keyed with the build
// node's token hash, which only that node and the hub know, so the hub hands it out and the
// build node checks it without storing anything. ponytail: never expires; scope is one app.
func PullToken(buildTokenHash, app string) string {
	m := hmac.New(sha256.New, []byte(buildTokenHash))
	io.WriteString(m, "registry pull walroos-app-"+app)
	return hex.EncodeToString(m.Sum(nil))
}

// Build builds the app and pushes it to this node's registry, for a prod node to pull.
func (a *Apps) Build(ctx context.Context, spec AppSpec, log io.Writer) (Release, error) {
	if !spec.Valid() || spec.Image != "" || spec.Pull != "" {
		return Release{}, ErrInvalidApp
	}
	unlock, err := a.lock(spec.Name)
	if err != nil {
		return Release{}, err
	}
	defer unlock()
	authFile, err := a.ensureRegistry(ctx)
	if err != nil {
		return Release{}, fmt.Errorf("registry: %w", err)
	}
	defer os.Remove(authFile)
	sha, image, err := build(ctx, spec, log)
	if err != nil {
		return Release{}, err
	}
	fmt.Fprintf(log, "pushing %s to this node's registry\n", image)
	if err := run(ctx, nil, "podman", "push", "--quiet", "--tls-verify=false", "--authfile", authFile, image, "docker://"+registryAddr+"/"+strings.TrimPrefix(image, "localhost/")); err != nil {
		return Release{}, err
	}
	// ponytail: the registry is never garbage-collected; each build adds only its own layers.
	pruneImages(context.WithoutCancel(ctx), spec.Name, image)
	return Release{Sha: sha, Image: image}, nil
}

// pull fetches the build of spec.Sha from the build node at spec.Pull. The host's podman
// can't reach the tailnet (tsnet is in this process), so it pulls from a local proxy that
// dials the build node and adds the token. The image ends up named as if built here.
func (a *Apps) pull(ctx context.Context, spec AppSpec, log io.Writer) (sha, image string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "http", spec.Pull, spec.Pull
			r.Out.Header.Set("Authorization", "Bearer "+spec.PullToken)
		},
		Transport: &http.Transport{DialContext: a.dial},
	}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	repo := "walroos-app-" + spec.Name + ":" + spec.Sha[:12]
	remote := ln.Addr().String() + "/" + repo
	fmt.Fprintf(log, "pulling %s from the build node %s\n", repo, spec.Pull)
	if err := run(ctx, nil, "podman", "pull", "--quiet", "--tls-verify=false", "docker://"+remote); err != nil {
		return "", "", err
	}
	image = "localhost/" + repo
	if _, err := podman(ctx, "tag", remote, image); err != nil {
		return "", "", err
	}
	podman(context.WithoutCancel(ctx), "untag", image, remote) // the proxy's port changes every pull
	return spec.Sha, image, nil
}

// RegistryHandler serves this node's registry to the tailnet: pulls only (GET/HEAD of a
// manifest or blob), with a PullToken for that app.
func (s *Server) RegistryHandler() http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: registryAddr})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/v2/" { // the API version check carries nothing
			// /v2/walroos-app-<name>/{manifests,blobs}/<ref>, nothing that path cleaning could move
			parts := strings.Split(r.URL.Path, "/")
			app, own := "", false
			if len(parts) == 5 && (parts[3] == "manifests" || parts[3] == "blobs") {
				app, own = strings.CutPrefix(parts[2], "walroos-app-")
			}
			token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !own || r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path ||
				subtle.ConstantTimeCompare([]byte(token), []byte(PullToken(string(s.tokenHash), app))) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		s.apps.regMu.Lock()
		pass := s.apps.regPass
		s.apps.regMu.Unlock()
		if pass == "" {
			http.Error(w, "no build on this node since its agent started", http.StatusServiceUnavailable)
			return
		}
		r.SetBasicAuth("walroos", pass) // the pull token never reaches the registry
		proxy.ServeHTTP(w, r)
	})
}

// ensureRegistry starts the registry container if it isn't running, or if this process doesn't
// know its password, and returns a podman auth file (0600) for pushing to it. Host network: it
// binds 127.0.0.1 only, and rootless port forwarding needs NAT pieces some kernels (WSL) lack.
// The password matters: a Dockerfile's `RUN --network=host` reaches the node's loopback.
func (a *Apps) ensureRegistry(ctx context.Context) (authFile string, err error) {
	a.regMu.Lock()
	defer a.regMu.Unlock()
	// Also recreate it when our password no longer works: something else (another agent on this
	// host, a test run) recreated the container with its own users file, and every push would
	// fail with "authentication required" until the agent restarts.
	if out, _ := podman(ctx, "inspect", "--format", "{{.State.Running}}", registryName); out != "true" || a.regPass == "" || !registryAuthOK(ctx, a.regPass) {
		if a.regPass, err = startRegistry(ctx); err != nil {
			return "", err
		}
	}
	f, err := os.CreateTemp("", "walroos-registry-auth-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()
	auth := base64.StdEncoding.EncodeToString([]byte("walroos:" + a.regPass))
	_, err = fmt.Fprintf(f, `{"auths": {%q: {"auth": %q}}}`, registryAddr, auth)
	return f.Name(), err
}

func startRegistry(ctx context.Context) (pass string, err error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "walroos", "registry")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// A random password per agent start: only its bcrypt hash touches disk, the agent keeps the
	// password in memory and hands it to podman push through a temp --authfile.
	pass = rand.Text()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "users"), []byte("walroos:"+string(hash)+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(registryConfig()), 0o600); err != nil {
		return "", err
	}
	podman(ctx, "rm", "-f", "--ignore", registryName)
	if _, err := podman(ctx, "run", "-d", "--name", registryName, "--network", "host", "--restart", "always",
		"-v", registryName+":/var/lib/registry", "-v", dir+":/etc/docker/registry:ro,Z", registryImage); err != nil {
		return "", err
	}
	for range 50 {
		if resp, err := http.Get("http://" + registryAddr + "/v2/"); err == nil {
			resp.Body.Close()
			return pass, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("%s didn't answer on %s", registryName, registryAddr)
}

// registryAuthOK reports whether the registry accepts pass. Any error counts as no.
func registryAuthOK(ctx context.Context, pass string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+registryAddr+"/v2/", nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth("walroos", pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// registryConfig replaces the image's /etc/docker/registry/config.yml: storage on the volume,
// listen only on registryAddr, and basic auth against the users file written next to it.
func registryConfig() string {
	return fmt.Sprintf(`version: 0.1
storage:
  filesystem:
    rootdirectory: /var/lib/registry
http:
  addr: %s
auth:
  htpasswd:
    realm: walroos
    path: /etc/docker/registry/users
`, registryAddr)
}
