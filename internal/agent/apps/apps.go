// Package apps deploys app releases on a node: clone and build (or pull, or take a build node's
// image from its registry), start next to the running release as a Quadlet, health check, swap.
// It also runs cloudflared, the node's Cloudflare Tunnel.
package apps

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
)

var (
	ErrBusy     = quadlet.ErrBusy
	ErrNotFound = errors.New("app not found on this node")
)

var imageIDRe = regexp.MustCompile(`^[0-9a-f]{64}$`) // what podman prints for an image

const keepImages = 3 // per app, for rollback

var healthTimeout = 90 * time.Second // tests shorten it

// Apps runs apps as Podman Quadlets: systemd restarts them, starts them on boot and keeps
// their logs in the journal.
type Apps struct {
	q     *quadlet.Host
	relay []string                                                          // `walroos node relay`, for health checks inside the container's netns
	dial  func(ctx context.Context, network, addr string) (net.Conn, error) // to the tailnet, for pulls

	regMu   sync.Mutex
	regPass string // the registry's password, made when this process (re)starts it; never on disk
}

func New(q *quadlet.Host) *Apps { return &Apps{q: q} }

// SetRelay sets the command that runs sshgw.Relay (`walroos node relay`); health checks use it.
func (a *Apps) SetRelay(relay []string) { a.relay = relay }

// SetDial sets how to reach other nodes (tsnet's Dial); a prod node pulls builds with it.
func (a *Apps) SetDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	a.dial = dial
}

// Deploy builds (or pulls from spec.Pull; nothing if spec.Image is set), starts the new container next to the old one,
// waits for it to answer on its port, then stops the old one. If the new one never gets
// healthy it is removed and the old one keeps serving. Progress goes to log.
func (a *Apps) Deploy(ctx context.Context, spec agent.AppSpec, log io.Writer) (agent.Release, error) {
	if !spec.Valid() {
		return agent.Release{}, agent.ErrInvalidApp
	}
	unlock, err := a.q.Lock(spec.Name)
	if err != nil {
		return agent.Release{}, err
	}
	defer unlock()
	if err := quadlet.EnsureNetwork(ctx, spec.Network); err != nil {
		return agent.Release{}, err
	}
	rel, err := a.image(ctx, spec, log)
	if err != nil {
		return agent.Release{}, err
	}
	old, err := a.units(spec.Name)
	if err != nil {
		return agent.Release{}, err
	}
	// Cleanup must finish even if the hub hangs up mid-deploy, or the new release is orphaned.
	sw := swap{a: a, bg: context.WithoutCancel(ctx), log: log, old: old, next: rel.Container, recreate: len(spec.Volumes) > 0}
	sw.stopOld()
	if why, err := a.bringUp(ctx, spec, rel, log); err != nil {
		return agent.Release{}, sw.undo(why, err)
	}
	sw.done()
	pruneImages(sw.bg, spec.Name, rel.Image)
	fmt.Fprintf(log, "%s is live (%s)\n", rel.Container, rel.Sha)
	return rel, nil
}

// image gets the release's image: from a build node's registry, a published image, a build of
// the repo, or (rollback) the one asked for.
func (a *Apps) image(ctx context.Context, spec agent.AppSpec, log io.Writer) (agent.Release, error) {
	rel := agent.Release{Image: spec.Image, Container: spec.Name + "-" + spec.ID}
	var err error
	switch {
	case spec.Pull != "":
		rel.Sha, rel.Image, err = a.pull(ctx, spec, log)
	case spec.Image == "" && spec.Source != "":
		rel.Sha, rel.Image, err = pullSource(ctx, spec, log)
	case spec.Image == "":
		rel.Sha, rel.Image, err = build(ctx, spec, log)
	default:
		_, rel.Sha, _ = strings.Cut(spec.Image, ":")
		fmt.Fprintf(log, "rollback: running %s, no build\n", spec.Image)
	}
	return rel, err
}

// bringUp starts the release, waits for its health check and routes the tunnel to it. On error
// why says which step failed.
func (a *Apps) bringUp(ctx context.Context, spec agent.AppSpec, rel agent.Release, log io.Writer) (why string, err error) {
	fmt.Fprintf(log, "starting %s\n", rel.Container)
	if err := a.start(ctx, spec, rel); err != nil {
		fmt.Fprint(log, host.Journal(context.WithoutCancel(ctx), rel.Container, 20))
		if host.KernelUpdated() { // the usual cause: say it instead of "systemctl: exit status 1"
			err = errors.New(agent.KernelUpdated.Title + ". " + agent.KernelUpdated.Fix)
		}
		return "couldn't start " + rel.Container, err
	}
	if err := a.waitHealthy(ctx, rel.Container, spec.Port, cmp.Or(spec.HealthPath, "/"), log); err != nil {
		fmt.Fprint(log, host.Journal(context.WithoutCancel(ctx), rel.Container, 20))
		return rel.Container + " never got healthy", err
	}
	if err := a.expose(ctx, spec.Name, rel.Container); err != nil {
		return "couldn't put " + rel.Container + " on " + quadlet.EdgeNetwork, err
	}
	return "", nil
}

// swap replaces the old releases with next. Two releases writing the same database files corrupt
// them, so an app with volumes (recreate) stops first and comes back if next fails. ponytail:
// downtime for every app with volumes, read-only ones too.
type swap struct {
	a        *Apps
	bg       context.Context
	log      io.Writer
	old      []string
	next     string
	recreate bool
}

func (s swap) stopOld() {
	if !s.recreate {
		return
	}
	for _, c := range s.old {
		fmt.Fprintf(s.log, "stopping %s first: the app has volumes\n", c)
		host.Run(s.bg, nil, "systemctl", "--user", "stop", c+".service")
	}
}

// undo removes next and starts the old releases again if they were stopped.
func (s swap) undo(why string, err error) error {
	fmt.Fprintf(s.log, "%s, removing it; the previous release keeps serving\n", why)
	s.a.q.Remove(s.bg, s.next)
	if s.recreate {
		for _, c := range s.old {
			fmt.Fprintf(s.log, "starting %s again\n", c)
			host.Run(s.bg, nil, "systemctl", "--user", "start", c+".service")
		}
	}
	return err
}

func (s swap) done() {
	for _, c := range s.old {
		fmt.Fprintf(s.log, "stopping previous release %s\n", c)
		s.a.q.Remove(s.bg, c)
	}
}

// build clones the repo at spec.Sha (or the branch head) and builds its Dockerfile.
func build(ctx context.Context, spec agent.AppSpec, log io.Writer) (sha, image string, err error) {
	dir, err := os.MkdirTemp("", "walroos-build-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)

	args := []string{"clone", "--single-branch"}
	if spec.Branch != "" {
		args = append(args, "--branch", spec.Branch)
	}
	fmt.Fprintf(log, "cloning %s %s\n", redact(spec.RepoURL), spec.Branch)
	if _, err := host.GitClone(ctx, args, spec.RepoURL, dir); err != nil {
		msg := strings.ReplaceAll(err.Error(), host.HTTPSURL(spec.RepoURL), redact(host.HTTPSURL(spec.RepoURL)))
		return "", "", errors.New(strings.ReplaceAll(msg, spec.RepoURL, redact(spec.RepoURL)))
	}
	if spec.Sha != "" {
		if err := host.Run(ctx, nil, "git", "-C", dir, "checkout", "--quiet", "--detach", spec.Sha); err != nil {
			return "", "", err
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	image = "localhost/walroos-app-" + spec.Name + ":" + sha[:12]
	fmt.Fprintf(log, "building %s at %s\n", image, sha[:12])
	if spec.Static != nil {
		return sha, image, buildStatic(ctx, spec, dir, image, log)
	}
	dockerfile, err := host.Inside(dir, filepath.Join(dir, cmp.Or(spec.Dockerfile, "Dockerfile")))
	if err != nil {
		return "", "", fmt.Errorf("dockerfile: %w", err)
	}
	return sha, image, podmanBuild(ctx, spec.Name, log, "-t", image, "-f", dockerfile, dir)
}

// podmanBuild runs podman build with args, the image labeled as app's.
func podmanBuild(ctx context.Context, app string, log io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "podman", append([]string{"build", "--label", quadlet.LabelApp + "=" + app}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman build: %w", err)
	}
	return nil
}

// pullSource pulls the published image and tags it as this app's own, by image id: rollback
// then runs that exact image even after the source tag moves.
func pullSource(ctx context.Context, spec agent.AppSpec, log io.Writer) (sha, image string, err error) {
	fmt.Fprintf(log, "pulling %s\n", spec.Source)
	id, err := host.Podman(ctx, "pull", "--quiet", "docker://"+spec.Source)
	if err != nil {
		return "", "", err
	}
	if !imageIDRe.MatchString(id) { // 64 hex: an image id
		return "", "", fmt.Errorf("podman pull: unexpected output %q", id)
	}
	image = "localhost/walroos-app-" + spec.Name + ":" + id[:12]
	if _, err := host.Podman(ctx, "tag", id, image); err != nil {
		return "", "", err
	}
	// ponytail: the source tag stays (the owner may use it too), so deleting the app leaves its
	// current image; older ones only have our tag and pruneImages frees them.
	return id[:12], image, nil
}

// start writes the Quadlet for rel (and its env file) and starts the unit systemd generates.
func (a *Apps) start(ctx context.Context, spec agent.AppSpec, rel agent.Release) error {
	if err := os.MkdirAll(a.q.Dir, 0o755); err != nil {
		return err
	}
	envFile := filepath.Join(a.q.Dir, rel.Container+".env")
	if err := writeEnv(envFile, spec.Env); err != nil {
		return err
	}
	guard, err := a.q.Guard(ctx, rel.Container)
	if err != nil {
		return err
	}
	mounts, err := a.mounts(ctx, spec)
	if err != nil {
		return err
	}
	unit := appUnit(spec, rel, envFile, guard, mounts)
	if err := os.WriteFile(filepath.Join(a.q.Dir, rel.Container+".container"), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := host.Run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return host.Run(ctx, nil, "systemctl", "--user", "start", rel.Container+".service")
}

// writeEnv writes the KEY=VALUE lines to a 0600 file: values never reach argv or the unit.
func writeEnv(path string, env []string) error {
	var b bytes.Buffer
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" && !strings.ContainsAny(kv, "\n\r") {
			fmt.Fprintln(&b, kv)
		}
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}

// appUnit is the release's Quadlet .container file.
func appUnit(spec agent.AppSpec, rel agent.Release, envFile, guard, mounts string) string {
	return fmt.Sprintf(`# Written by walroos. Rollback and redeploy replace it; don't edit.
[Unit]
Description=walroos app %[1]s (%[3]s)

[Container]
Image=%[4]s
ContainerName=%[2]s
Network=%[5]s:alias=%[1]s
Label=%[6]s=%[1]s
Label=%[7]s=%[3]s
EnvironmentFile=%[8]s
NoNewPrivileges=true
# ponytail: same limits for every app; per-app limits when one needs more
Memory=2g
PidsLimit=2048
%[10]s
[Quadlet]
# The default waits for network-online.target, which many hosts (WSL) never reach.
DefaultDependencies=false

[Service]
Restart=always
%[9]s

[Install]
WantedBy=default.target
`, spec.Name, rel.Container, rel.Sha, rel.Image, cmp.Or(spec.Network, agent.AppNetwork), quadlet.LabelApp, quadlet.LabelSha, envFile, guard, mounts)
}

// mounts creates the app's volumes and returns their Quadlet Volume= lines. ponytail: no
// SELinux relabel (:Z); rootless podman on the hosts we target doesn't enforce it.
func (a *Apps) mounts(ctx context.Context, spec agent.AppSpec) (string, error) {
	var b strings.Builder
	for _, v := range spec.Volumes {
		src := "walroos-app-" + spec.Name + "-" + v.Name
		if v.HostPath != "" {
			var err error
			if src, err = a.bindDir(v.HostPath); err != nil {
				return "", err
			}
		} else if err := host.EnsureVolume(ctx, src, quadlet.LabelApp, spec.Name); err != nil { // labeled, so a purge finds it
			return "", err
		}
		ro := ""
		if v.RO {
			ro = ":ro"
		}
		fmt.Fprintf(&b, "Volume=%s:%s%s\n", src, v.Target, ro)
	}
	return b.String(), nil
}

// bindDir makes rel under the data root and returns its real path. A symlink can't take it
// outside the root, or to the root itself (every app's data).
func (a *Apps) bindDir(rel string) (string, error) {
	if err := os.MkdirAll(a.q.Data, 0o755); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(a.q.Data) // the root itself may point at another disk
	if err != nil {
		return "", err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer r.Close()
	if err := r.MkdirAll(rel, 0o755); err != nil {
		return "", fmt.Errorf("volume %s: %w", rel, err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return "", fmt.Errorf("volume %s: a symlink leads outside %s", rel, a.q.Data)
	}
	return dir, nil
}

// waitHealthy GETs path on the container's port from inside its network namespace until it
// answers 2xx/3xx. The app's image needs no curl: the relay is our own binary.
func (a *Apps) waitHealthy(ctx context.Context, container string, port int, path string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	fmt.Fprintf(log, "health check: GET %s%s\n", addr, path)
	var last error
	for i := 0; ; i++ {
		code, err := a.get(ctx, container, addr, path)
		if err == nil && code >= 200 && code < 400 {
			fmt.Fprintf(log, "health check: %d\n", code)
			return nil
		}
		if last = err; err == nil {
			last = fmt.Errorf("answered %d", code)
		}
		if i%10 == 9 {
			fmt.Fprintf(log, "health check: still waiting (%v)\n", last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("health check: no 2xx/3xx on %s%s within %s: %v", addr, path, healthTimeout, last)
		case <-time.After(time.Second):
		}
	}
}

func (a *Apps) get(ctx context.Context, container, addr, path string) (int, error) {
	pid, err := host.Podman(ctx, "inspect", "--format", "{{.State.Pid}}", container)
	if err != nil || pid == "" || pid == "0" {
		return 0, fmt.Errorf("%s is not running", container)
	}
	args := append([]string{"nsenter", "--net=/proc/" + pid + "/ns/net", "--"}, a.relay...)
	args = append(args, "", addr) // no chroot: 127.0.0.1 needs no name lookup
	if os.Geteuid() != 0 {
		args = append([]string{"podman", "unshare"}, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	out := ask(stdin, stdout, path)
	stdout.Close() // the relay gets EPIPE and exits
	werr := cmd.Wait()
	out = bytes.TrimPrefix(out, []byte{0}) // the relay's "connected" byte
	status, _, _ := bufio.NewReader(bytes.NewReader(out)).ReadLine()
	if f := strings.Fields(string(status)); len(f) >= 2 && strings.HasPrefix(f[0], "HTTP/") {
		return strconv.Atoi(f[1])
	}
	if werr != nil {
		return 0, fmt.Errorf("connect: %w", werr)
	}
	return 0, fmt.Errorf("not an HTTP answer: %q", string(status))
}

// ask sends the health check's request on w and reads the answer from r. It closes w (the relay
// then half-closes the connection) only after the answer: Bun drops a request whose client
// half-closed before it was answered.
func ask(w io.WriteCloser, r io.Reader, path string) []byte {
	defer w.Close()
	if _, err := io.WriteString(w, "GET "+path+" HTTP/1.0\r\nHost: localhost\r\nUser-Agent: walroos-health\r\nConnection: close\r\n\r\n"); err != nil {
		return nil
	}
	out, _ := io.ReadAll(io.LimitReader(r, 4096)) // the status line is enough; an endless body must not fill memory
	return out
}

// units lists the Quadlet containers of app, by file name.
func (a *Apps) units(app string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(a.q.Dir, app+"-*.container"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && bytes.Contains(b, []byte("\nLabel="+quadlet.LabelApp+"="+app+"\n")) { // not another app's "<app>-x"
			names = append(names, strings.TrimSuffix(filepath.Base(f), ".container"))
		}
	}
	return names, nil
}

// List returns the running release of every app on this node.
func (a *Apps) List(ctx context.Context) ([]agent.App, error) {
	files, err := filepath.Glob(filepath.Join(a.q.Dir, "*.container"))
	if err != nil {
		return nil, err
	}
	list := []agent.App{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		app, sha, image := quadlet.UnitValue(b, "Label="+quadlet.LabelApp+"="), quadlet.UnitValue(b, "Label="+quadlet.LabelSha+"="), quadlet.UnitValue(b, "Image=")
		if app == "" {
			continue // not ours
		}
		c := strings.TrimSuffix(filepath.Base(f), ".container")
		state, _ := exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", "ActiveState", "--value", c+".service").Output()
		list = append(list, agent.App{Name: app, Container: c, Sha: sha, Image: image, State: strings.TrimSpace(string(state))})
	}
	return list, nil
}

// Logs returns the last lines of the app's running release.
func (a *Apps) Logs(ctx context.Context, app string, lines int) (string, error) {
	units, err := a.units(app)
	if err != nil || len(units) == 0 {
		return "", ErrNotFound
	}
	// The container's own output; the unit's journal also has systemd and podman lifecycle lines.
	out, _ := exec.CommandContext(ctx, "journalctl", "--user", "CONTAINER_NAME="+units[len(units)-1], "-n", strconv.Itoa(lines), "--no-pager", "-o", "cat").Output()
	return strings.ReplaceAll(string(out), "\n\n", "\n"), nil // podman keeps the app's newline in MESSAGE
}

// Delete stops every release of app and removes its images. purge also removes its named
// volumes; directories under the data root always stay.
func (a *Apps) Delete(ctx context.Context, app string, purge bool) error {
	unlock, err := a.q.Lock(app) // else a deploy mid-build would start its release after this
	if err != nil {
		return err
	}
	defer unlock()
	units, err := a.units(app)
	if err != nil {
		return err
	}
	for _, c := range units {
		a.q.Remove(ctx, c)
	}
	pruneImages(ctx, app, "")
	if purge {
		vols, err := host.Podman(ctx, "volume", "ls", "-q", "--filter", "label="+quadlet.LabelApp+"="+app)
		if err != nil {
			return err
		}
		for _, v := range strings.Fields(vols) {
			if _, err := host.Podman(ctx, "volume", "rm", v); err != nil {
				return err
			}
		}
	}
	return nil
}

// pruneImages keeps the newest keepImages images of app (and keep), for rollback.
func pruneImages(ctx context.Context, app, keep string) {
	out, err := host.Podman(ctx, "images", "--filter", "reference=localhost/walroos-app-"+app, "--format", "{{.Repository}}:{{.Tag}}", "--sort", "created")
	if err != nil {
		return
	}
	imgs := strings.Fields(out) // podman lists newest first
	n := keepImages
	if keep == "" {
		n = 0
	}
	for i, img := range imgs {
		if i >= n && img != keep {
			host.Podman(ctx, "rmi", "--ignore", img)
		}
	}
}

// redact hides the token in an https://user:token@host URL: the log goes to the UI and MCP.
func redact(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = url.User("***")
		return u.String()
	}
	return raw
}
