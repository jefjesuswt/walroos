//go:build linux

package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAppDeployPodman deploys a tiny app as a Quadlet, then a broken release (the old one must
// keep serving), a rollback, and deletes it. Needs podman, systemd --user and the image
// docker.io/library/python:3-alpine. In WSL create the network first (no NAT there):
//
//	podman network create --internal walroos
//	WALROOS_PODMAN_TEST=1 go test ./internal/agent -run AppDeployPodman -v
func TestAppDeployPodman(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1")
	}
	ctx := context.Background()
	t.Setenv("WALROOS_TEST_RELAY", "1") // the relay command is this binary
	healthTimeout = 15 * time.Second
	a := NewApps()
	a.relay = []string{os.Args[0]}
	name := fmt.Sprintf("t%d", time.Now().UnixNano()%1e6)
	t.Cleanup(func() { a.Delete(context.Background(), name) })

	repo := t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(cmd, page string) string {
		os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM docker.io/library/python:3-alpine\nWORKDIR /srv\nCOPY index.html .\nCMD "+cmd+"\n"), 0o644)
		os.WriteFile(filepath.Join(repo, "index.html"), []byte(page), 0o644)
		git("add", "-A")
		git("commit", "-qm", page)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	good := commit(`["python3", "-m", "http.server", "8000"]`, "v1")

	var log strings.Builder
	spec := AppSpec{Name: name, ID: "a1", RepoURL: repo, Branch: "main", Port: 8000}
	v1, err := a.Deploy(ctx, spec, &log)
	if err != nil {
		t.Fatalf("deploy v1: %v\n%s", err, log.String())
	}
	if v1.Sha != good || v1.Container != name+"-a1" || !strings.Contains(log.String(), "health check: 200") {
		t.Fatalf("v1 = %+v\n%s", v1, log.String())
	}
	assertLive(t, a, name, v1.Container)
	if got := edgeGet(t, name); got != "v1" {
		t.Fatalf("through %s: %q, want v1", EdgeNetwork, got)
	}
	// The alias is in the unit too, so it survives a restart (crash, reboot).
	if err := run(ctx, nil, "systemctl", "--user", "restart", v1.Container+".service"); err != nil {
		t.Fatal(err)
	}
	if err := a.waitHealthy(ctx, v1.Container, 8000, "/", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := edgeGet(t, name); got != "v1" {
		t.Fatalf("after a restart: %q, want v1", got)
	}

	assertGuarded(t)

	// A release that never answers: removed, v1 keeps serving.
	commit(`["sleep", "300"]`, "broken")
	log.Reset()
	spec.ID, spec.Sha = "a2", ""
	if _, err := a.Deploy(ctx, spec, &log); err == nil || !strings.Contains(err.Error(), "health check") {
		t.Fatalf("broken deploy: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "previous release keeps serving") {
		t.Errorf("log doesn't say v1 stays:\n%s", log.String())
	}
	assertLive(t, a, name, v1.Container)

	// Deploy a specific older sha, then roll back to v1's image without building.
	commit(`["python3", "-m", "http.server", "8000"]`, "v3")
	spec.ID, spec.Sha = "a3", good
	v1again, err := a.Deploy(ctx, spec, io.Discard)
	if err != nil || v1again.Sha != good {
		t.Fatalf("deploy by sha: %+v %v", v1again, err)
	}
	spec.ID, spec.Sha = "a4", ""
	v3, err := a.Deploy(ctx, spec, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	log.Reset()
	back, err := a.Deploy(ctx, AppSpec{Name: name, ID: "a5", Image: v1.Image, Port: 8000}, &log)
	if err != nil || back.Sha != v1.Image[strings.LastIndex(v1.Image, ":")+1:] || strings.Contains(log.String(), "building") {
		t.Fatalf("rollback: %+v %v\n%s", back, err, log.String())
	}
	assertLive(t, a, name, back.Container)
	if _, err := podman(ctx, "image", "exists", v3.Image); err != nil {
		t.Errorf("v3's image was pruned too early: %v", err)
	}

	if logs, err := a.Logs(ctx, name, 50); err != nil || !strings.Contains(logs, "GET /") {
		t.Errorf("logs: %v %q", err, logs)
	}

	// Bad specs never touch podman.
	for _, bad := range []AppSpec{{Name: "Bad", ID: "x", RepoURL: repo, Port: 1}, {Name: name, ID: "../x", RepoURL: repo, Port: 1}, {Name: name, ID: "x", RepoURL: repo}} {
		if _, err := a.Deploy(ctx, bad, io.Discard); err != ErrInvalidApp {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	// The hub hangs up mid health check: the new release is removed, the old one keeps serving.
	// Meanwhile a delete is refused, or it would race the deploy.
	commit(`["sh", "-c", "sleep 60"]`, "slow")
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error)
	go func() {
		_, err := a.Deploy(cctx, AppSpec{Name: name, ID: "a6", RepoURL: repo, Branch: "main", Port: 8000}, io.Discard)
		done <- err
	}()
	for range 100 {
		if _, err := podman(ctx, "container", "exists", name+"-a6"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := a.Delete(ctx, name); err != ErrAppBusy {
		t.Errorf("delete during deploy: %v", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled deploy succeeded")
	}
	assertLive(t, a, name, back.Container)

	if err := a.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if list, _ := a.List(ctx); len(appsNamed(list, name)) != 0 {
		t.Errorf("after delete: %+v", list)
	}
	if out, _ := podman(ctx, "images", "-q", "--filter", "reference=localhost/walroos-app-"+name); out != "" {
		t.Errorf("images left: %s", out)
	}
}

// assertLive checks that container is the app's only release, active and answering.
func assertLive(t *testing.T, a *Apps, app, container string) {
	t.Helper()
	list, err := a.List(context.Background())
	mine := appsNamed(list, app)
	if err != nil || len(mine) != 1 || mine[0].Container != container || mine[0].State != "active" {
		t.Fatalf("releases of %s: %+v %v, want only %s active", app, mine, err, container)
	}
	if code, err := a.get(context.Background(), container, "127.0.0.1:8000", "/"); code != 200 {
		t.Fatalf("%s answers %d %v", container, code, err)
	}
	if got := edgeGet(t, app); got == "" {
		t.Fatalf("%s doesn't answer as %s on %s", container, app, EdgeNetwork)
	}
}

// edgeGet fetches / from the app's alias on EdgeNetwork, as cloudflared would.
func edgeGet(t *testing.T, app string) string {
	t.Helper()
	out, err := podman(context.Background(), "run", "--rm", "--network", EdgeNetwork, "docker.io/library/python:3-alpine",
		"wget", "-qO-", "-T", "5", "http://"+app+":8000/")
	if err != nil {
		t.Logf("edge get %s: %v", app, err)
	}
	return out
}

// assertGuarded checks that a container on the app networks can't reach this host's private
// address (a stand-in for the LAN), while public egress still works.
func assertGuarded(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if out, err := podman(ctx, "unshare", "--rootless-netns", "nft", "list", "table", "inet", "walroos_guard"); err != nil || !strings.Contains(out, "reject") {
		t.Fatalf("guard table: %v\n%s", err, out)
	}
	var lan string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && ip.IP.IsPrivate() {
			lan = ip.IP.String()
			break
		}
	}
	if lan == "" {
		t.Log("no private address on this host; skipping the LAN half")
	} else {
		ln, err := net.Listen("tcp", lan+":0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Write([]byte("HTTP/1.0 200 OK\r\n\r\nlan\n"))
				c.Close()
			}
		}()
		for _, n := range []string{AppNetwork, EdgeNetwork} {
			out, err := podman(ctx, "run", "--rm", "--network", n, "docker.io/library/python:3-alpine",
				"wget", "-qO-", "-T", "5", "http://"+ln.Addr().String()+"/")
			if err == nil || strings.Contains(out, "lan") {
				t.Errorf("a container on %s reached %s: %q", n, ln.Addr(), out)
			}
		}
	}
	// Internet: only where the host has it (WSL's networks are --internal: no NAT there).
	if _, err := podman(ctx, "run", "--rm", "--network", "pasta", "docker.io/library/python:3-alpine", "wget", "-qO/dev/null", "-T", "5", "https://www.cloudflare.com/cdn-cgi/trace"); err != nil {
		t.Log("no internet; skipping the public half")
		return
	}
	if out, err := podman(ctx, "network", "inspect", EdgeNetwork, "--format", "{{.Internal}}"); err == nil && out == "false" {
		if out, err := podman(ctx, "run", "--rm", "--network", EdgeNetwork, "docker.io/library/python:3-alpine", "wget", "-qO-", "-T", "5", "https://www.cloudflare.com/cdn-cgi/trace"); err != nil {
			t.Errorf("public egress from %s: %v %s", EdgeNetwork, err, out)
		}
	}
}

func appsNamed(list []App, name string) []App {
	var out []App
	for _, x := range list {
		if x.Name == name {
			out = append(out, x)
		}
	}
	return out
}

// A build node builds and pushes to its registry; a prod node pulls through RegistryHandler.
// Both are this host here, so the local image is removed between the two.
func TestAppBuildPullPodman(t *testing.T) {
	if os.Getenv("WALROOS_PODMAN_TEST") == "" {
		t.Skip("set WALROOS_PODMAN_TEST=1")
	}
	ctx := context.Background()
	t.Setenv("WALROOS_TEST_RELAY", "1")
	a := NewApps()
	a.relay = []string{os.Args[0]}
	a.dial = (&net.Dialer{}).DialContext
	name := fmt.Sprintf("t%d", time.Now().UnixNano()%1e6)
	t.Cleanup(func() { a.Delete(context.Background(), name) })

	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM docker.io/library/python:3-alpine\nWORKDIR /srv\nRUN echo built > index.html\nCMD [\"python3\", \"-m\", \"http.server\", \"8000\"]\n"), 0o644)
	if out, err := exec.Command("sh", "-c", "cd "+repo+" && git init -q -b main && git add -A && git -c user.email=t@t -c user.name=t commit -qm v1").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}

	var log strings.Builder
	spec := AppSpec{Name: name, ID: "b1", RepoURL: repo, Branch: "main", Port: 8000}
	build := NewServer(nil, "build-node-token") // the build node: its registry password lives in its Apps
	rel, err := build.apps.Build(ctx, spec, &log)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	if _, err := podman(ctx, "rmi", rel.Image); err != nil {
		t.Fatal(err)
	}

	reg := httptest.NewServer(build.RegistryHandler())
	t.Cleanup(reg.Close)
	spec.Pull, spec.Sha = strings.TrimPrefix(reg.URL, "http://"), rel.Sha

	spec.PullToken = PullToken(TokenHash("build-node-token"), "other")
	if _, err := a.Deploy(ctx, spec, &log); err == nil {
		t.Fatal("pulled with another app's token")
	}
	spec.PullToken = PullToken(TokenHash("build-node-token"), name)
	got, err := a.Deploy(ctx, spec, &log)
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, log.String())
	}
	if got.Image != rel.Image || got.Sha != rel.Sha {
		t.Fatalf("release %+v, built %+v", got, rel)
	}
	assertLive(t, a, name, name+"-b1")
	if out, _ := podman(ctx, "images", "--format", "{{.Repository}}", "--filter", "reference=*/walroos-app-"+name); strings.TrimSpace(out) != "localhost/walroos-app-"+name {
		t.Errorf("images left: %q", out) // no 127.0.0.1:<port>/... tags
	}
}
