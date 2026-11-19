package agent

import (
	"bufio"
	"bytes"
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
)

var (
	ErrInvalidApp  = errors.New("invalid app: name must be a DNS label, port 1-65535, sha hex, branch and dockerfile without spaces or a leading '-', health_path a URL path")
	ErrAppBusy     = errors.New("a deploy or delete of this app is already running on this node")
	ErrAppNotFound = errors.New("app not found on this node")
)

const (
	AppNetwork = "walroos" // apps reach each other here by container name; apps publish no host ports
	labelApp   = "walroos.app"
	labelSha   = "walroos.sha"
	keepImages = 3 // per app, for rollback
)

var healthTimeout = 90 * time.Second // tests shorten it

// AppSpec is one deploy. Image set means rollback: run that image, skip clone and build.
type AppSpec struct {
	Name       string   `json:"name"`
	ID         string   `json:"id"` // deployment id from the hub; the container is <name>-<id>
	RepoURL    string   `json:"repo_url"`
	Branch     string   `json:"branch"`
	Sha        string   `json:"sha"`        // "" deploys the branch head
	Dockerfile string   `json:"dockerfile"` // relative to the repo; default Dockerfile
	Port       int      `json:"port"`
	HealthPath string   `json:"health_path"` // default /
	Image      string   `json:"image"`
	Env        []string `json:"env"`
	Pull       string   `json:"pull,omitempty"`       // build node's registry (tailnet host:port): pull Sha from it, don't build
	PullToken  string   `json:"pull_token,omitempty"` // PullToken for this app on that node
}

// Release is what a successful deploy left running.
type Release struct {
	Sha       string `json:"sha"`
	Image     string `json:"image"`
	Container string `json:"container"`
}

// App is an app's state on this node: the release that's running, if any.
type App struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	Sha       string `json:"sha"`
	Image     string `json:"image"`
	State     string `json:"state"` // systemd ActiveState: active, activating, failed...
}

// Apps runs apps as Podman Quadlets: systemd restarts them, starts them on boot and keeps
// their logs in the journal.
type Apps struct {
	mu    sync.Mutex
	busy  map[string]bool
	relay []string                                                          // `walroos node relay`, for health checks inside the container's netns
	dir   string                                                            // where the Quadlet files go; systemd's generator reads it
	dial  func(ctx context.Context, network, addr string) (net.Conn, error) // to the tailnet, for pulls

	regMu   sync.Mutex
	regPass string // the registry's password, made when this process (re)starts it; never on disk
}

func NewApps() *Apps {
	cfg, _ := os.UserConfigDir()
	return &Apps{busy: map[string]bool{}, dir: filepath.Join(cfg, "containers", "systemd")}
}

var (
	deployIDRe   = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	shaRe        = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	refRe        = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]{0,199}$`)    // branch, dockerfile: no leading "-" (git/podman options)
	healthPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,511}$`) // goes into the raw HTTP request line
	tokenRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Valid reports whether spec is safe to hand to git, podman and the health check. The hub
// checks it too, to answer 400 instead of a failed deployment.
func (spec AppSpec) Valid() bool {
	opt := func(re *regexp.Regexp, v string) bool { return v == "" || re.MatchString(v) }
	ownImage := "localhost/walroos-app-" + spec.Name + ":"
	tag, isOwn := strings.CutPrefix(spec.Image, ownImage)
	return workspaceNameRe.MatchString(spec.Name) && deployIDRe.MatchString(spec.ID) && spec.Port >= 1 && spec.Port <= 65535 &&
		(spec.RepoURL != "" || spec.Image != "") && !strings.HasPrefix(spec.RepoURL, "-") &&
		opt(shaRe, spec.Sha) && opt(refRe, spec.Branch) && opt(refRe, spec.Dockerfile) && opt(healthPathRe, spec.HealthPath) &&
		(spec.Image == "" || isOwn && len(tag) == 12 && shaRe.MatchString(tag)) && // rollback runs only this app's own builds
		(spec.Pull == "" || validHostPort(spec.Pull) && tokenRe.MatchString(spec.PullToken) && len(spec.Sha) >= 12 && spec.Image == "")
}

func validHostPort(v string) bool {
	host, port, err := net.SplitHostPort(v)
	return err == nil && host != "" && !strings.ContainsAny(host, "/@") && port != ""
}

// Deploy builds (or pulls from spec.Pull; nothing if spec.Image is set), starts the new container next to the old one,
// waits for it to answer on its port, then stops the old one. If the new one never gets
// healthy it is removed and the old one keeps serving. Progress goes to log.
func (a *Apps) Deploy(ctx context.Context, spec AppSpec, log io.Writer) (Release, error) {
	if !spec.Valid() {
		return Release{}, ErrInvalidApp
	}
	unlock, err := a.lock(spec.Name)
	if err != nil {
		return Release{}, err
	}
	defer unlock()
	// Cleanup must finish even if the hub hangs up mid-deploy, or the new release is orphaned.
	bg := context.WithoutCancel(ctx)

	if err := ensureNetwork(ctx, AppNetwork); err != nil {
		return Release{}, err
	}
	rel := Release{Image: spec.Image, Container: spec.Name + "-" + spec.ID}
	if spec.Pull != "" {
		if rel.Sha, rel.Image, err = a.pull(ctx, spec, log); err != nil {
			return Release{}, err
		}
	} else if spec.Image == "" {
		if rel.Sha, rel.Image, err = build(ctx, spec, log); err != nil {
			return Release{}, err
		}
	} else {
		_, rel.Sha, _ = strings.Cut(spec.Image, ":")
		fmt.Fprintf(log, "rollback: running %s, no build\n", spec.Image)
	}

	old, err := a.units(spec.Name)
	if err != nil {
		return Release{}, err
	}
	fmt.Fprintf(log, "starting %s\n", rel.Container)
	if err := a.start(ctx, spec, rel); err != nil {
		a.remove(bg, rel.Container)
		return Release{}, err
	}
	if err := a.waitHealthy(ctx, rel.Container, spec.Port, cmpOr(spec.HealthPath, "/"), log); err != nil {
		fmt.Fprintf(log, "%s never got healthy, removing it; the previous release keeps serving\n%s", rel.Container, journal(bg, rel.Container, 20))
		a.remove(bg, rel.Container)
		return Release{}, err
	}
	if err := a.expose(ctx, spec.Name, rel.Container); err != nil {
		fmt.Fprintf(log, "couldn't put %s on %s, removing it; the previous release keeps serving\n", rel.Container, EdgeNetwork)
		a.remove(bg, rel.Container)
		return Release{}, err
	}
	for _, c := range old {
		fmt.Fprintf(log, "stopping previous release %s\n", c)
		a.remove(bg, c)
	}
	pruneImages(bg, spec.Name, rel.Image)
	fmt.Fprintf(log, "%s is live (%s)\n", rel.Container, rel.Sha)
	return rel, nil
}

// build clones the repo at spec.Sha (or the branch head) and builds its Dockerfile.
func build(ctx context.Context, spec AppSpec, log io.Writer) (sha, image string, err error) {
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
	if err := run(ctx, []string{"GIT_TERMINAL_PROMPT=0"}, "git", append(args, "--", httpsURL(spec.RepoURL), dir)...); err != nil {
		msg := strings.ReplaceAll(err.Error(), httpsURL(spec.RepoURL), redact(httpsURL(spec.RepoURL)))
		return "", "", errors.New(strings.ReplaceAll(msg, spec.RepoURL, redact(spec.RepoURL)))
	}
	if spec.Sha != "" {
		if err := run(ctx, nil, "git", "-C", dir, "checkout", "--quiet", "--detach", spec.Sha); err != nil {
			return "", "", err
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	dockerfile, err := inside(dir, filepath.Join(dir, cmpOr(spec.Dockerfile, "Dockerfile")))
	if err != nil {
		return "", "", fmt.Errorf("dockerfile: %w", err)
	}
	image = "localhost/walroos-app-" + spec.Name + ":" + sha[:12]
	fmt.Fprintf(log, "building %s at %s\n", image, sha[:12])
	cmd := exec.CommandContext(ctx, "podman", "build", "--label", labelApp+"="+spec.Name, "-t", image, "-f", dockerfile, dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("podman build: %w", err)
	}
	return sha, image, nil
}

// start writes the Quadlet for rel (and its env file) and starts the unit systemd generates.
func (a *Apps) start(ctx context.Context, spec AppSpec, rel Release) error {
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return err
	}
	envFile := filepath.Join(a.dir, rel.Container+".env")
	var env bytes.Buffer
	for _, kv := range spec.Env {
		if k, _, ok := strings.Cut(kv, "="); ok && k != "" && !strings.ContainsAny(kv, "\n\r") {
			fmt.Fprintln(&env, kv)
		}
	}
	if err := os.WriteFile(envFile, env.Bytes(), 0o600); err != nil { // values never reach argv or the unit
		return err
	}
	guard, err := a.guard(ctx, rel.Container)
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`# Written by walroos. Rollback and redeploy replace it; don't edit.
[Unit]
Description=walroos app %[1]s (%[3]s)

[Container]
Image=%[4]s
ContainerName=%[2]s
Network=%[5]s
Label=%[6]s=%[1]s
Label=%[7]s=%[3]s
EnvironmentFile=%[8]s
NoNewPrivileges=true
# ponytail: same limits for every app; per-app limits when one needs more
Memory=2g
PidsLimit=2048

[Quadlet]
# The default waits for network-online.target, which many hosts (WSL) never reach.
DefaultDependencies=false

[Service]
Restart=always
%[9]s

[Install]
WantedBy=default.target
`, spec.Name, rel.Container, rel.Sha, rel.Image, AppNetwork, labelApp, labelSha, envFile, guard)
	if err := os.WriteFile(filepath.Join(a.dir, rel.Container+".container"), []byte(unit), 0o644); err != nil {
		return err
	}
	if err := run(ctx, nil, "systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return run(ctx, nil, "systemctl", "--user", "start", rel.Container+".service")
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
	pid, err := podman(ctx, "inspect", "--format", "{{.State.Pid}}", container)
	if err != nil || pid == "" || pid == "0" {
		return 0, fmt.Errorf("%s is not running", container)
	}
	args := append([]string{"nsenter", "--net=/proc/" + pid + "/ns/net", "--"}, a.relay...)
	args = append(args, "", addr) // no chroot: 127.0.0.1 needs no name lookup
	if os.Geteuid() != 0 {
		args = append([]string{"podman", "unshare"}, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = strings.NewReader("GET " + path + " HTTP/1.0\r\nHost: localhost\r\nUser-Agent: walroos-health\r\n\r\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	out, _ := io.ReadAll(io.LimitReader(stdout, 4096)) // the status line is enough; an endless body must not fill memory
	stdout.Close()                                     // the relay gets EPIPE and exits
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

// units lists the Quadlet containers of app, by file name.
func (a *Apps) units(app string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(a.dir, app+"-*.container"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && bytes.Contains(b, []byte("\nLabel="+labelApp+"="+app+"\n")) { // not another app's "<app>-x"
			names = append(names, strings.TrimSuffix(filepath.Base(f), ".container"))
		}
	}
	return names, nil
}

// remove stops a release and deletes its unit and env file. The image stays, for rollback.
func (a *Apps) remove(ctx context.Context, container string) {
	run(ctx, nil, "systemctl", "--user", "stop", container+".service")
	os.Remove(filepath.Join(a.dir, container+".container"))
	os.Remove(filepath.Join(a.dir, container+".env"))
	run(ctx, nil, "systemctl", "--user", "daemon-reload")
	run(ctx, nil, "systemctl", "--user", "reset-failed", container+".service")
	podman(ctx, "rm", "-f", "--ignore", container)
}

// List returns the running release of every app on this node.
func (a *Apps) List(ctx context.Context) ([]App, error) {
	files, err := filepath.Glob(filepath.Join(a.dir, "*.container"))
	if err != nil {
		return nil, err
	}
	list := []App{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		app, sha, image := unitValue(b, "Label="+labelApp+"="), unitValue(b, "Label="+labelSha+"="), unitValue(b, "Image=")
		if app == "" {
			continue // not ours
		}
		c := strings.TrimSuffix(filepath.Base(f), ".container")
		state, _ := exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", "ActiveState", "--value", c+".service").Output()
		list = append(list, App{Name: app, Container: c, Sha: sha, Image: image, State: strings.TrimSpace(string(state))})
	}
	return list, nil
}

// Logs returns the last lines of the app's running release.
func (a *Apps) Logs(ctx context.Context, app string, lines int) (string, error) {
	units, err := a.units(app)
	if err != nil || len(units) == 0 {
		return "", ErrAppNotFound
	}
	// The container's own output; the unit's journal also has systemd and podman lifecycle lines.
	out, _ := exec.CommandContext(ctx, "journalctl", "--user", "CONTAINER_NAME="+units[len(units)-1], "-n", strconv.Itoa(lines), "--no-pager", "-o", "cat").Output()
	return strings.ReplaceAll(string(out), "\n\n", "\n"), nil // podman keeps the app's newline in MESSAGE
}

// Delete stops every release of app and removes its images.
func (a *Apps) Delete(ctx context.Context, app string) error {
	unlock, err := a.lock(app) // else a deploy mid-build would start its release after this
	if err != nil {
		return err
	}
	defer unlock()
	units, err := a.units(app)
	if err != nil {
		return err
	}
	for _, c := range units {
		a.remove(ctx, c)
	}
	pruneImages(ctx, app, "")
	return nil
}

// lock marks app busy: one deploy or delete at a time per app.
func (a *Apps) lock(app string) (unlock func(), err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy[app] {
		return nil, ErrAppBusy
	}
	a.busy[app] = true
	return func() { a.mu.Lock(); delete(a.busy, app); a.mu.Unlock() }, nil
}

func journal(ctx context.Context, container string, lines int) string {
	out, _ := exec.CommandContext(ctx, "journalctl", "--user", "-u", container+".service", "-n", strconv.Itoa(lines), "--no-pager", "-o", "cat").Output()
	return string(out)
}

// pruneImages keeps the newest keepImages images of app (and keep), for rollback.
func pruneImages(ctx context.Context, app, keep string) {
	out, err := podman(ctx, "images", "--filter", "reference=localhost/walroos-app-"+app, "--format", "{{.Repository}}:{{.Tag}}", "--sort", "created")
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
			podman(ctx, "rmi", "--ignore", img)
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

func unitValue(unit []byte, prefix string) string {
	for _, l := range strings.Split(string(unit), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), prefix); ok {
			return v
		}
	}
	return ""
}

// ensureNetwork creates a network the first time.
func ensureNetwork(ctx context.Context, name string) error {
	if _, err := podman(ctx, "network", "exists", name); err == nil {
		return nil
	}
	_, err := podman(ctx, "network", "create", name)
	return err
}
