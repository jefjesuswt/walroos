//go:build linux

package apps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/agenttest"
	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
)

// TestStaticSitePodman deploys two static sites with no Dockerfile: plain HTML served as it is
// (its dotfiles not), and a Node build that reads the app's env vars, served as an SPA. Needs
// the images docker.io/library/busybox:stable and docker.io/library/node:22-slim
// (NODE_VERSION picks it).
func TestStaticSitePodman(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1")
	}
	ctx := context.Background()
	t.Setenv("WALROOS_TEST_RELAY", "1")
	healthTimeout = 30 * time.Second
	a := New(quadlet.New())
	a.relay = []string{os.Args[0]}
	serverBinary(t)

	get := func(container, path string) (int, string) {
		t.Helper()
		out, err := exec.Command("podman", "run", "--rm", "--network", agent.AppNetwork, "docker.io/library/busybox:stable",
			"sh", "-c", "wget -q -S -O - http://"+container+":8080"+path+" 2>&1; true").CombinedOutput()
		if err != nil {
			t.Fatalf("wget %s: %v %s", path, err, out)
		}
		code := 0
		fmt.Sscanf(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0], "HTTP/1.1 %d", &code)
		return code, string(out)
	}
	deploy := func(files map[string]string, b agent.StaticBuild, env ...string) (string, agent.Release) {
		t.Helper()
		name := fmt.Sprintf("t%d", time.Now().UnixNano()%1e6)
		t.Cleanup(func() { a.Delete(context.Background(), name, true) })
		var log strings.Builder
		rel, err := a.Deploy(ctx, agent.AppSpec{Name: name, ID: "s1", RepoURL: agenttest.GitRepo(t, files), Port: agent.StaticPort, Static: &b, Env: env}, &log)
		if err != nil {
			t.Fatalf("deploy: %v\n%s", err, log.String())
		}
		return log.String(), rel
	}

	log, rel := deploy(map[string]string{"index.html": "plain v1", ".env": "SECRET=1"}, agent.StaticBuild{})
	if code, body := get(rel.Container, "/"); code != 200 || !strings.Contains(body, "plain v1") {
		t.Fatalf("/ = %d %s\n%s", code, body, log)
	}
	if code, _ := get(rel.Container, "/.env"); code != 404 {
		t.Fatalf("/.env = %d", code)
	}

	build := `mkdir -p dist && node -e "require('fs').writeFileSync('dist/index.html', 'built for ' + process.env.GREETING)"`
	log, rel = deploy(map[string]string{"package.json": `{"name": "s", "scripts": {"build": "` + strings.ReplaceAll(build, `"`, `\"`) + `"}}`, "index.html": "source"},
		agent.StaticBuild{SPA: true}, "GREETING=it's me", "NODE_VERSION=22")
	if code, body := get(rel.Container, "/some/route"); code != 200 || !strings.Contains(body, "built for it's me") {
		t.Fatalf("/some/route = %d %s\n%s", code, body, log)
	}
	if !strings.Contains(log, "static site: node 22 (NODE_VERSION) with npm") || !strings.Contains(log, "serving dist") {
		t.Fatalf("log:\n%s", log)
	}
	out, _ := exec.Command("podman", "history", "--no-trunc", rel.Image).CombinedOutput()
	if strings.Contains(string(out), "it's me") {
		t.Fatalf("an env value is in the image's history:\n%s", out)
	}
}

// serverBinary builds cmd/walroos for static sites' images: the test binary isn't walroos.
func serverBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "walroos")
	cmd := exec.Command("go", "build", "-o", exe, "../../../cmd/walroos")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	ServerBinary = func() (string, error) { return exe, nil }
}
