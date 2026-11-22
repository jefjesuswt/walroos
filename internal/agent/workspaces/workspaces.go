// Package workspaces runs dev workspaces on a node: clone (or init) the repo, build or pull the
// devcontainer image, run the container with its volumes, and rebuild or delete it.
package workspaces

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/shared/names"
)

var (
	ErrNotFound = errors.New("workspace not found")
	ErrExists   = errors.New("workspace already exists on this node")
	ErrBusy     = errors.New("workspace is being created or deleted")
	ErrInvalid  = errors.New("workspace name must be a DNS label and repo_url (or init) is required")
)

const (
	labelWorkspace = "walroos.workspace"
	labelUser      = "walroos.user"     // uid:gid sessions run as
	labelFolder    = "walroos.folder"   // where the repo is mounted; sessions start here
	labelAgentDir  = "walroos.agentdir" // where agentDir is mounted; absent on workspaces from before the SSH gateway
	agentMount     = "/run/walroos"     // container path of agentDir
	createTimeout  = 30 * time.Minute
)

// Workspaces manages workspace containers through the podman CLI. Podman is the source of
// truth: a workspace is the container ws-<name> labeled walroos.workspace=<name>, plus its
// volumes ws-<name>-src (the repo) and ws-<name>-home ($HOME, so ~/.claude etc. survive rebuilds).
type Workspaces struct {
	mu     sync.Mutex
	busy   map[string]string // name → creating | deleting
	failed map[string]string // name → last create error
	// ponytail: busy/failed live in memory, an agent restart forgets a failure; the hub keeps its own state.
}

func New() *Workspaces {
	return &Workspaces{busy: map[string]string{}, failed: map[string]string{}}
}

// Create starts creating a workspace in the background; poll Get for the outcome.
func (w *Workspaces) Create(ctx context.Context, spec agent.WorkspaceSpec, env []string) (agent.Workspace, error) {
	if !names.DNSLabel(spec.Name) || (spec.RepoURL == "") == (len(spec.Init) == 0) {
		return agent.Workspace{}, ErrInvalid
	}
	for p := range spec.Init {
		if !filepath.IsLocal(p) || len(spec.Init) > 16 {
			return agent.Workspace{}, fmt.Errorf("%w: init files must be at most 16, with relative paths inside the repo", ErrInvalid)
		}
	}
	exists, err := w.containerExists(ctx, spec.Name)
	if err != nil {
		return agent.Workspace{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, busy := w.busy[spec.Name]; busy || exists {
		return agent.Workspace{}, ErrExists
	}
	w.busy[spec.Name] = "creating"
	delete(w.failed, spec.Name)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), createTimeout)
		defer cancel()
		err := create(ctx, spec, env)

		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.busy, spec.Name)
		if err != nil {
			w.failed[spec.Name] = err.Error()
		}
	}()
	return agent.Workspace{Name: spec.Name, State: "creating"}, nil
}

func create(ctx context.Context, spec agent.WorkspaceSpec, env []string) error {
	name := spec.Name
	src, home := "ws-"+name+"-src", "ws-"+name+"-home"
	for _, v := range []string{src, home} {
		if err := host.EnsureVolume(ctx, v, labelWorkspace, name); err != nil {
			return err
		}
	}
	repo, err := checkout(ctx, spec, src)
	if err != nil {
		return err
	}
	dc, err := readDevcontainer(repo, spec.DevcontainerPath, spec.Devcontainer)
	if err != nil {
		return err
	}
	c, err := prepare(ctx, spec, repo, dc)
	if err != nil {
		return err
	}
	if err := runContainer(ctx, c, dc, env, src, home); err != nil {
		return err
	}
	linkRepo(ctx, c)
	if dc != nil && len(dc.PostCreateCommand) > 0 {
		if _, err := host.Podman(ctx, append([]string{"exec", "--user", c.user(), "-w", c.folder, "ws-" + name}, dc.PostCreateCommand...)...); err != nil {
			return fmt.Errorf("postCreateCommand: %w", err)
		}
	}
	saveSpec(ctx, home, spec) // so rebuild knows the branch and devcontainer path, not just the remote
	return nil
}

// checkout clones the repo (or inits a new project's) with the host's git straight into the src
// volume, unless it's there already; it returns the volume's path. With --userns=keep-id the files
// end up owned by the workspace user inside the container.
func checkout(ctx context.Context, spec agent.WorkspaceSpec, src string) (string, error) {
	repo, err := host.Podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", src)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); !errors.Is(err, os.ErrNotExist) {
		return repo, nil
	}
	if spec.RepoURL == "" {
		if err := initRepo(ctx, repo, spec.Init); err != nil {
			return "", fmt.Errorf("git init: %w", err)
		}
		return repo, nil
	}
	args := []string{"clone"}
	if spec.Branch != "" {
		args = append(args, "--branch", spec.Branch)
	}
	url, err := host.GitClone(ctx, args, spec.RepoURL, repo, spec.GitToken)
	if err != nil {
		return "", fmt.Errorf("clone %s: %w", spec.RepoURL, err)
	}
	if url != spec.RepoURL { // push from inside the workspace with the user's own key
		if err := host.Run(ctx, nil, "git", "-C", repo, "remote", "set-url", "origin", spec.RepoURL); err != nil {
			return "", err
		}
	}
	return repo, nil
}

// container is what a workspace's container runs as and where.
type container struct {
	name, image  string
	uid, gid     string
	home, folder string // the user's $HOME and the repo, inside the container
}

func (c container) user() string { return c.uid + ":" + c.gid }

// prepare builds or pulls the image and finds the user sessions run as: the image's
// devcontainer.metadata label first, devcontainer.json overrides it.
func prepare(ctx context.Context, spec agent.WorkspaceSpec, repo string, dc *devcontainer) (container, error) {
	c := container{name: spec.Name, folder: "/workspaces/" + repoName(cmp.Or(spec.RepoURL, spec.Name))} // a new project's folder is its name
	var err error
	if c.image, err = resolveImage(ctx, spec.Name, repo, dc); err != nil {
		return c, err
	}
	label, _ := host.Podman(ctx, "image", "inspect", "--format", `{{index .Labels "devcontainer.metadata"}}`, c.image)
	remoteUser := metadataRemoteUser(label)
	if dc != nil && dc.RemoteUser != "" {
		remoteUser = dc.RemoteUser
	}
	c.uid, c.gid, c.home, err = probeUser(ctx, c.image, remoteUser)
	return c, err
}

// runContainer starts the workspace container with its volumes, env and devcontainer mounts.
func runContainer(ctx context.Context, c container, dc *devcontainer, env []string, src, home string) error {
	// Services on the node that a workspace needs (a database, a broker) join DevNetwork and are
	// reachable by container name; pasta's default network can't reach the host's ports at all.
	if err := host.EnsureNetwork(ctx, agent.DevNetwork); err != nil {
		return err
	}
	sockets := AgentDir(c.name)
	if err := os.MkdirAll(sockets, 0o700); err != nil {
		return err
	}
	args := []string{"run", "-d", "--name", "ws-" + c.name, "--hostname", c.name, "--network", agent.DevNetwork,
		"--label", labelWorkspace + "=" + c.name, "--label", labelUser + "=" + c.user(), "--label", labelFolder + "=" + c.folder,
		"--label", labelAgentDir + "=" + agentMount,
		"--user", c.user(), "-v", src + ":" + c.folder, "-v", home + ":" + c.home, "-v", sockets + ":" + agentMount + ":z", "-w", c.folder}
	if c.uid != "0" { // map the host user to the workspace user; rootless default already maps it to root
		args = append(args, "--userns=keep-id:uid="+c.uid+",gid="+c.gid)
	}
	if len(env) > 0 {
		f, err := writeEnvFile(env)
		if err != nil {
			return err
		}
		defer os.Remove(f) // podman reads it at start; the values never show up in argv
		args = append(args, "--env-file", f)
	}
	extra, err := devcontainerArgs(ctx, c.name, dc)
	if err != nil {
		return err
	}
	// Like devcontainers' overrideCommand: keep the container alive, sessions come in via exec.
	args = append(append(args, extra...), "--entrypoint", "/bin/sh", c.image, "-c", `trap "exit 0" TERM; while sleep 1000 & wait $!; do :; done`)
	_, err = host.Podman(ctx, args...)
	return err
}

// devcontainerArgs are the devcontainer's env and mounts, each mount's volume made (labeled, so
// Delete removes it).
func devcontainerArgs(ctx context.Context, name string, dc *devcontainer) ([]string, error) {
	if dc == nil {
		return nil, nil
	}
	var args []string
	for k, v := range dc.ContainerEnv {
		args = append(args, "-e", k+"="+v)
	}
	for _, m := range dc.Mounts {
		opt, vol := m.scoped(name)
		if vol != "" {
			if err := host.EnsureVolume(ctx, vol, labelWorkspace, name); err != nil {
				return nil, err
			}
		}
		args = append(args, "--mount", opt)
	}
	return args, nil
}

// linkRepo puts a ~/<repo> link to the repo: editors' folder pickers (Zed's Open Folder) start in
// $HOME. Something the user already has at that path wins; the link lives in the home volume, so
// a rebuild finds it.
func linkRepo(ctx context.Context, c container) {
	link := path.Join(c.home, path.Base(c.folder))
	if _, err := host.Podman(ctx, "exec", "--user", c.user(), "ws-"+c.name, "sh", "-c",
		`test -e "$1" || test -L "$1" || ln -s "$2" "$1"`, "sh", link, c.folder); err != nil {
		log.Printf("workspace %s: link %s to the repo: %v", c.name, link, err)
	}
}

// resolveImage builds build.dockerfile, or makes sure image (or the base image) is present.
func resolveImage(ctx context.Context, name, repo string, dc *devcontainer) (string, error) {
	if dc == nil {
		return ensureBaseImage(ctx)
	}
	if dc.Build.Dockerfile != "" {
		dockerfile, err := host.Inside(repo, filepath.Join(dc.dir, dc.Build.Dockerfile))
		if err != nil {
			return "", fmt.Errorf("build.dockerfile: %w", err)
		}
		buildCtx, err := host.Inside(repo, filepath.Join(dc.dir, cmp.Or(dc.Build.Context, ".")))
		if err != nil {
			return "", fmt.Errorf("build.context: %w", err)
		}
		image := "localhost/walroos-ws-" + name
		_, err = host.Podman(ctx, "build", "-t", image, "-f", dockerfile, buildCtx)
		return image, err
	}
	if _, err := host.Podman(ctx, "image", "exists", dc.Image); err != nil {
		if _, err := host.Podman(ctx, "pull", "-q", dc.Image); err != nil {
			return "", err
		}
	}
	return dc.Image, nil
}

// probeUser asks the image for the uid, gid and $HOME of user ("" = the image's default user).
func probeUser(ctx context.Context, image, user string) (uid, gid, home string, err error) {
	args := []string{"run", "--rm", "--entrypoint", "/bin/sh"}
	if user != "" {
		args = append(args, "--user", user)
	}
	out, err := host.Podman(ctx, append(args, image, "-c", `id -u; id -g; echo "$HOME"`)...)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect user %q in %s: %w", user, image, err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return "", "", "", fmt.Errorf("inspect user %q in %s: unexpected output %q", user, image, out)
	}
	uid, gid = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if _, err := strconv.Atoi(uid); err != nil {
		return "", "", "", fmt.Errorf("inspect user %q in %s: bad uid %q", user, image, uid)
	}
	home = "/root"
	if len(lines) > 2 && strings.HasPrefix(lines[2], "/") && lines[2] != "/" {
		home = strings.TrimSpace(lines[2])
	}
	return uid, gid, home, nil
}

func (w *Workspaces) Get(ctx context.Context, name string) (agent.Workspace, error) {
	list, err := w.list(ctx, name)
	if err != nil {
		return agent.Workspace{}, err
	}
	if len(list) == 0 {
		return agent.Workspace{}, ErrNotFound
	}
	return list[0], nil
}

func (w *Workspaces) List(ctx context.Context) ([]agent.Workspace, error) {
	return w.list(ctx, "")
}

// list merges podman's containers with in-flight and failed workspaces. name == "" means all.
func (w *Workspaces) list(ctx context.Context, name string) ([]agent.Workspace, error) {
	// Snapshot in-flight work BEFORE asking podman. The other order races: podman ps sees a
	// half-started container ("created"), creation finishes and clears busy, and the stale
	// snapshot is reported as "stopped". This way, not busy at snapshot time means podman's
	// answer is from after creation.
	w.mu.Lock()
	failed, busy := maps.Clone(w.failed), maps.Clone(w.busy)
	w.mu.Unlock()

	byName, err := containers(ctx, name)
	if err != nil {
		return nil, err
	}
	// What this agent is doing or last failed at wins over what podman shows.
	overlay := func(states map[string]string, state func(string) (string, string)) {
		for n, v := range states {
			if name == "" || n == name {
				ws := byName[n]
				ws.Name = n
				ws.State, ws.Error = state(v)
				byName[n] = ws
			}
		}
	}
	overlay(failed, func(msg string) (string, string) { return "failed", msg })
	overlay(busy, func(state string) (string, string) { return state, "" })

	list := make([]agent.Workspace, 0, len(byName))
	for _, ws := range byName {
		list = append(list, ws)
	}
	return list, nil
}

// containers reads the workspace containers podman has (one, or all if name is ""), by name.
func containers(ctx context.Context, name string) (map[string]agent.Workspace, error) {
	filter := labelWorkspace
	if name != "" {
		filter += "=" + name
	}
	out, err := host.Podman(ctx, "ps", "-a", "--filter", "label="+filter, "--format", "json")
	if err != nil {
		return nil, err
	}
	var list []struct {
		State  string
		Image  string
		Labels map[string]string
	}
	if err := json.Unmarshal([]byte(cmp.Or(out, "[]")), &list); err != nil {
		return nil, fmt.Errorf("podman ps: %w", err)
	}
	byName := map[string]agent.Workspace{}
	for _, c := range list {
		ws := agent.Workspace{Name: c.Labels[labelWorkspace], State: "stopped", Image: c.Image, User: c.Labels[labelUser],
			Folder: c.Labels[labelFolder], AgentDir: c.Labels[labelAgentDir]}
		if c.State == "running" {
			ws.State = "running"
		}
		byName[ws.Name] = ws
	}
	return byName, nil
}

func (w *Workspaces) Start(ctx context.Context, name string) error {
	os.MkdirAll(AgentDir(name), 0o700) // the runtime dir is emptied on reboot, and the bind mount needs it
	return w.containerOp(ctx, name, "start", "ws-"+name)
}

func (w *Workspaces) Stop(ctx context.Context, name string) error {
	return w.containerOp(ctx, name, "stop", "-t", "5", "ws-"+name)
}

func (w *Workspaces) containerOp(ctx context.Context, name string, args ...string) error {
	w.mu.Lock()
	_, busy := w.busy[name]
	w.mu.Unlock()
	if busy {
		return ErrBusy
	}
	if ok, err := w.containerExists(ctx, name); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	_, err := host.Podman(ctx, args...)
	return err
}

// Rebuild recreates the container and any image built for it, keeping the volumes: the clone
// and $HOME (mise runtimes, ~/.claude, caches) survive. It's how a changed devcontainer, a new
// base image or a broken container is picked up.
func (w *Workspaces) Rebuild(ctx context.Context, name string, env []string) (agent.Workspace, error) {
	if _, err := host.Podman(ctx, "volume", "inspect", "ws-"+name+"-src"); err != nil {
		return agent.Workspace{}, ErrNotFound
	}
	w.mu.Lock()
	if _, busy := w.busy[name]; busy {
		w.mu.Unlock()
		return agent.Workspace{}, ErrBusy
	}
	w.busy[name] = "creating" // rebuild shows as creating: same state the hub allows
	delete(w.failed, name)
	w.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), createTimeout)
		defer cancel()
		err := rebuild(ctx, name, env)

		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.busy, name)
		if err != nil {
			w.failed[name] = err.Error()
		}
	}()
	return agent.Workspace{Name: name, State: "creating"}, nil
}

func rebuild(ctx context.Context, name string, env []string) error {
	src, home := "ws-"+name+"-src", "ws-"+name+"-home"
	repo, err := host.Podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", src)
	if err != nil {
		return ErrNotFound
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return fmt.Errorf("%s has no clone in its source volume", name)
	}

	spec := loadSpec(ctx, home)
	if spec.RepoURL == "" && len(spec.Init) == 0 { // created before the spec file existed: the remote is enough
		spec.RepoURL = gitRemote(ctx, repo)
	}
	spec.Name = name
	if spec.RepoURL == "" && len(spec.Init) == 0 {
		return fmt.Errorf("%s has no remote origin to rebuild from", name)
	}

	// Remove the container, not the volumes; drop the image built from the repo so it rebuilds.
	if ids, err := host.Podman(ctx, "ps", "-aq", "--filter", "label="+labelWorkspace+"="+name); err != nil {
		return err
	} else if ids != "" {
		if _, err := host.Podman(ctx, append([]string{"rm", "-f", "-t", "5"}, strings.Fields(ids)...)...); err != nil {
			return err
		}
	}
	host.Podman(ctx, "rmi", "-f", "--ignore", "localhost/walroos-ws-"+name)

	return create(ctx, spec, env)
}

// Delete removes the container, both volumes and any image built for the workspace. Idempotent.
func (w *Workspaces) Delete(ctx context.Context, name string) error {
	w.mu.Lock()
	if _, busy := w.busy[name]; busy {
		w.mu.Unlock()
		return ErrBusy
	}
	w.busy[name] = "deleting"
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.busy, name)
		delete(w.failed, name)
		w.mu.Unlock()
	}()

	// Only what carries our label: a container or volume that merely shares the ws-<name>
	// naming belongs to someone else and must survive.
	label := "label=" + labelWorkspace + "=" + name
	if ids, err := host.Podman(ctx, "ps", "-aq", "--filter", label); err != nil {
		return err
	} else if ids != "" {
		if _, err := host.Podman(ctx, append([]string{"rm", "-f", "-t", "5"}, strings.Fields(ids)...)...); err != nil {
			return err
		}
	}
	if vols, err := host.Podman(ctx, "volume", "ls", "-q", "--filter", label); err != nil {
		return err
	} else if vols != "" {
		if _, err := host.Podman(ctx, append([]string{"volume", "rm", "-f"}, strings.Fields(vols)...)...); err != nil {
			return err
		}
	}
	host.Podman(ctx, "rmi", "-f", "--ignore", "localhost/walroos-ws-"+name) // only exists for build.dockerfile
	os.RemoveAll(AgentDir(name))
	return nil
}

// agentDir is the host directory mounted at agentMount, where the SSH gateway puts forwarded
// ssh-agent sockets. It lives in the user's runtime dir: private, and short enough for unix
// socket paths (108 bytes max).
// ponytail: the /tmp fallback (no XDG_RUNTIME_DIR, e.g. no login session) trusts that nobody
// else created it first; run the agent as a systemd user service, which sets the runtime dir.
func AgentDir(name string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "walroos-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, "walroos", name)
}

func (w *Workspaces) containerExists(ctx context.Context, name string) (bool, error) {
	out, err := host.Podman(ctx, "ps", "-aq", "--filter", "label="+labelWorkspace+"="+name)
	return out != "", err
}

// ready returns the workspace if it can take sessions, starting it if it was stopped.
func (w *Workspaces) Ready(ctx context.Context, name string) (agent.Workspace, error) {
	ws, err := w.Get(ctx, name)
	if err != nil {
		return ws, err
	}
	switch ws.State {
	case "running":
		return ws, nil
	case "stopped":
		if err := w.Start(ctx, name); err != nil {
			return ws, fmt.Errorf("start workspace: %w", err)
		}
		ws.State = "running"
		return ws, nil
	case "failed":
		return ws, fmt.Errorf("workspace %s failed: %s", name, ws.Error)
	default:
		return ws, fmt.Errorf("workspace %s is %s, try again in a moment", name, ws.State)
	}
}
