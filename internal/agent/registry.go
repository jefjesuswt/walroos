package agent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// A build node keeps the images it builds in a registry (distribution) bound to localhost.
// The tailnet reaches it only through RegistryHandler: read-only, one app per token.
const (
	RegistryPort  = "5000" // on the tailnet
	registryAddr  = "127.0.0.1:7075"
	registryName  = "walroos-registry"
	registryImage = "docker.io/library/registry:2"
)

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
	if err := ensureRegistry(ctx); err != nil {
		return Release{}, fmt.Errorf("registry: %w", err)
	}
	sha, image, err := build(ctx, spec, log)
	if err != nil {
		return Release{}, err
	}
	fmt.Fprintf(log, "pushing %s to this node's registry\n", image)
	if err := run(ctx, nil, "podman", "push", "--quiet", "--tls-verify=false", image, "docker://"+registryAddr+"/"+strings.TrimPrefix(image, "localhost/")); err != nil {
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
		r.Header.Del("Authorization")
		proxy.ServeHTTP(w, r)
	})
}

// ensureRegistry starts the registry container if it isn't running. Host network: it binds
// 127.0.0.1 only, and rootless port forwarding needs NAT pieces some kernels (WSL) lack.
func ensureRegistry(ctx context.Context) error {
	if out, _ := podman(ctx, "inspect", "--format", "{{.State.Running}}", registryName); out == "true" {
		return nil
	}
	podman(ctx, "rm", "-f", "--ignore", registryName)
	if _, err := podman(ctx, "run", "-d", "--name", registryName, "--network", "host", "--restart", "always",
		"-e", "REGISTRY_HTTP_ADDR="+registryAddr, "-v", registryName+":/var/lib/registry", registryImage); err != nil {
		return err
	}
	for range 50 {
		if resp, err := http.Get("http://" + registryAddr + "/v2/"); err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("%s didn't answer on %s", registryName, registryAddr)
}
