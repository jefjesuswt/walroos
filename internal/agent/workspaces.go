package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceExists   = errors.New("workspace already exists on this node")
	ErrWorkspaceBusy     = errors.New("workspace is being created or deleted")
	ErrInvalidWorkspace  = errors.New("workspace name must be a DNS label and repo_url is required")
)

const (
	labelWorkspace = "walroos.workspace"
	labelUser      = "walroos.user"   // uid:gid sessions run as
	labelFolder    = "walroos.folder" // where the repo is mounted; sessions start here
	createTimeout  = 30 * time.Minute
)

// WorkspaceSpec is what the hub sends to create a workspace.
type WorkspaceSpec struct {
	Name             string `json:"name"`
	RepoURL          string `json:"repo_url"`
	Branch           string `json:"branch"`            // "" = the repo's default branch
	DevcontainerPath string `json:"devcontainer_path"` // "" = standard locations, else base image
}

// Workspace is the agent's view of a workspace, derived from podman plus in-flight work.
type Workspace struct {
	Name   string `json:"name"`
	State  string `json:"state"` // creating, running, stopped, failed, deleting
	Image  string `json:"image,omitempty"`
	User   string `json:"user,omitempty"`   // uid:gid
	Folder string `json:"folder,omitempty"` // repo path inside the container
	Error  string `json:"error,omitempty"`
}

// Workspaces manages workspace containers through the podman CLI. Podman is the source of
// truth: a workspace is the container ws-<name> labeled walroos.workspace=<name>, plus its
// volumes ws-<name>-src (the repo) and ws-<name>-home ($HOME, so ~/.claude etc. survive rebuilds).
type Workspaces struct {
	mu     sync.Mutex
	busy   map[string]string // name → creating | deleting
	failed map[string]string // name → last create error
	// ponytail: busy/failed live in memory, an agent restart forgets a failure; the hub keeps its own state.
}

func NewWorkspaces() *Workspaces {
	return &Workspaces{busy: map[string]string{}, failed: map[string]string{}}
}

var workspaceNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Create starts creating a workspace in the background; poll Get for the outcome.
func (w *Workspaces) Create(ctx context.Context, spec WorkspaceSpec) (Workspace, error) {
	if !workspaceNameRe.MatchString(spec.Name) || spec.RepoURL == "" {
		return Workspace{}, ErrInvalidWorkspace
	}
	exists, err := w.containerExists(ctx, spec.Name)
	if err != nil {
		return Workspace{}, err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, busy := w.busy[spec.Name]; busy || exists {
		return Workspace{}, ErrWorkspaceExists
	}
	w.busy[spec.Name] = "creating"
	delete(w.failed, spec.Name)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), createTimeout)
		defer cancel()
		err := create(ctx, spec)

		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.busy, spec.Name)
		if err != nil {
			w.failed[spec.Name] = err.Error()
		}
	}()
	return Workspace{Name: spec.Name, State: "creating"}, nil
}

func create(ctx context.Context, spec WorkspaceSpec) error {
	name := spec.Name
	src, home := "ws-"+name+"-src", "ws-"+name+"-home"
	for _, v := range []string{src, home} {
		if err := ensureVolume(ctx, v, name); err != nil {
			return err
		}
	}

	// Clone with the host's git straight into the volume. With --userns=keep-id below the
	// files end up owned by the workspace user inside the container.
	repo, err := podman(ctx, "volume", "inspect", "--format", "{{.Mountpoint}}", src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, os.ErrNotExist) {
		args := []string{"clone"}
		if spec.Branch != "" {
			args = append(args, "--branch", spec.Branch)
		}
		args = append(args, "--", spec.RepoURL, repo)
		// ponytail: no credentials yet, so only public repos (or a token in the URL) clone
		if err := run(ctx, []string{"GIT_TERMINAL_PROMPT=0"}, "git", args...); err != nil {
			return fmt.Errorf("clone %s: %w", spec.RepoURL, err)
		}
	}

	dc, err := readDevcontainer(repo, spec.DevcontainerPath)
	if err != nil {
		return err
	}
	image, err := resolveImage(ctx, name, repo, dc)
	if err != nil {
		return err
	}

	// The image's devcontainer.metadata label first, devcontainer.json overrides it.
	label, _ := podman(ctx, "image", "inspect", "--format", `{{index .Labels "devcontainer.metadata"}}`, image)
	remoteUser := metadataRemoteUser(label)
	if dc != nil && dc.RemoteUser != "" {
		remoteUser = dc.RemoteUser
	}
	uid, gid, homeDir, err := probeUser(ctx, image, remoteUser)
	if err != nil {
		return err
	}

	folder := "/workspaces/" + repoName(spec.RepoURL)
	user := uid + ":" + gid
	args := []string{"run", "-d", "--name", "ws-" + name, "--hostname", name,
		"--label", labelWorkspace + "=" + name, "--label", labelUser + "=" + user, "--label", labelFolder + "=" + folder,
		"--user", user, "-v", src + ":" + folder, "-v", home + ":" + homeDir, "-w", folder}
	if uid != "0" { // map the host user to the workspace user; rootless default already maps it to root
		args = append(args, "--userns=keep-id:uid="+uid+",gid="+gid)
	}
	if dc != nil {
		for k, v := range dc.ContainerEnv {
			args = append(args, "-e", k+"="+v)
		}
		for _, m := range dc.Mounts {
			args = append(args, "--mount", string(m))
		}
	}
	// Like devcontainers' overrideCommand: keep the container alive, sessions come in via exec.
	args = append(args, "--entrypoint", "/bin/sh", image, "-c", `trap "exit 0" TERM; while sleep 1000 & wait $!; do :; done`)
	if _, err := podman(ctx, args...); err != nil {
		return err
	}

	if dc != nil && len(dc.PostCreateCommand) > 0 {
		execArgs := append([]string{"exec", "--user", user, "-w", folder, "ws-" + name}, dc.PostCreateCommand...)
		if _, err := podman(ctx, execArgs...); err != nil {
			return fmt.Errorf("postCreateCommand: %w", err)
		}
	}
	return nil
}

// resolveImage builds build.dockerfile, or makes sure image (or the base image) is present.
func resolveImage(ctx context.Context, name, repo string, dc *devcontainer) (string, error) {
	if dc == nil {
		return ensureBaseImage(ctx)
	}
	if dc.Build.Dockerfile != "" {
		dockerfile, err := inside(repo, filepath.Join(dc.dir, dc.Build.Dockerfile))
		if err != nil {
			return "", fmt.Errorf("build.dockerfile: %w", err)
		}
		buildCtx, err := inside(repo, filepath.Join(dc.dir, cmpOr(dc.Build.Context, ".")))
		if err != nil {
			return "", fmt.Errorf("build.context: %w", err)
		}
		image := "localhost/walroos-ws-" + name
		_, err = podman(ctx, "build", "-t", image, "-f", dockerfile, buildCtx)
		return image, err
	}
	if _, err := podman(ctx, "image", "exists", dc.Image); err != nil {
		if _, err := podman(ctx, "pull", "-q", dc.Image); err != nil {
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
	out, err := podman(ctx, append(args, image, "-c", `id -u; id -g; echo "$HOME"`)...)
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

func (w *Workspaces) Get(ctx context.Context, name string) (Workspace, error) {
	list, err := w.list(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	if len(list) == 0 {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return list[0], nil
}

func (w *Workspaces) List(ctx context.Context) ([]Workspace, error) {
	return w.list(ctx, "")
}

// list merges podman's containers with in-flight and failed workspaces. name == "" means all.
func (w *Workspaces) list(ctx context.Context, name string) ([]Workspace, error) {
	filter := labelWorkspace
	if name != "" {
		filter += "=" + name
	}
	// Snapshot in-flight work BEFORE asking podman. The other order races: podman ps sees a
	// half-started container ("created"), creation finishes and clears busy, and the stale
	// snapshot is reported as "stopped". This way, not busy at snapshot time means podman's
	// answer is from after creation.
	w.mu.Lock()
	failed, busy := maps.Clone(w.failed), maps.Clone(w.busy)
	w.mu.Unlock()

	out, err := podman(ctx, "ps", "-a", "--filter", "label="+filter, "--format", "json")
	if err != nil {
		return nil, err
	}
	var containers []struct {
		State  string
		Image  string
		Labels map[string]string
	}
	if err := json.Unmarshal([]byte(cmpOr(out, "[]")), &containers); err != nil {
		return nil, fmt.Errorf("podman ps: %w", err)
	}

	byName := map[string]Workspace{}
	for _, c := range containers {
		ws := Workspace{Name: c.Labels[labelWorkspace], State: "stopped", Image: c.Image, User: c.Labels[labelUser], Folder: c.Labels[labelFolder]}
		if c.State == "running" {
			ws.State = "running"
		}
		byName[ws.Name] = ws
	}

	for n, msg := range failed {
		if name == "" || n == name {
			ws := byName[n]
			ws.Name, ws.State, ws.Error = n, "failed", msg
			byName[n] = ws
		}
	}
	for n, state := range busy {
		if name == "" || n == name {
			ws := byName[n]
			ws.Name, ws.State, ws.Error = n, state, ""
			byName[n] = ws
		}
	}

	list := make([]Workspace, 0, len(byName))
	for _, ws := range byName {
		list = append(list, ws)
	}
	return list, nil
}

func (w *Workspaces) Start(ctx context.Context, name string) error {
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
		return ErrWorkspaceBusy
	}
	if ok, err := w.containerExists(ctx, name); err != nil {
		return err
	} else if !ok {
		return ErrWorkspaceNotFound
	}
	_, err := podman(ctx, args...)
	return err
}

// Delete removes the container, both volumes and any image built for the workspace. Idempotent.
func (w *Workspaces) Delete(ctx context.Context, name string) error {
	w.mu.Lock()
	if _, busy := w.busy[name]; busy {
		w.mu.Unlock()
		return ErrWorkspaceBusy
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
	if ids, err := podman(ctx, "ps", "-aq", "--filter", label); err != nil {
		return err
	} else if ids != "" {
		if _, err := podman(ctx, append([]string{"rm", "-f", "-t", "5"}, strings.Fields(ids)...)...); err != nil {
			return err
		}
	}
	if vols, err := podman(ctx, "volume", "ls", "-q", "--filter", label); err != nil {
		return err
	} else if vols != "" {
		if _, err := podman(ctx, append([]string{"volume", "rm", "-f"}, strings.Fields(vols)...)...); err != nil {
			return err
		}
	}
	podman(ctx, "rmi", "-f", "--ignore", "localhost/walroos-ws-"+name) // only exists for build.dockerfile
	return nil
}

// ensureVolume creates the workspace volume, or reuses it if it's ours (a retried create keeps
// the clone). A volume with that name but without our label belongs to someone else: refuse.
func ensureVolume(ctx context.Context, volume, workspace string) error {
	owner, err := podman(ctx, "volume", "inspect", "--format", `{{index .Labels "`+labelWorkspace+`"}}`, volume)
	if err != nil { // doesn't exist
		_, err := podman(ctx, "volume", "create", "--label", labelWorkspace+"="+workspace, volume)
		return err
	}
	if owner != workspace {
		return fmt.Errorf("volume %s already exists and isn't this workspace's; remove or rename it", volume)
	}
	return nil
}

func (w *Workspaces) containerExists(ctx context.Context, name string) (bool, error) {
	out, err := podman(ctx, "ps", "-aq", "--filter", "label="+labelWorkspace+"="+name)
	return out != "", err
}

// repoName is the folder name for a repo URL: github.com/me/app.git → app.
func repoName(url string) string {
	n := strings.TrimSuffix(path.Base(strings.TrimRight(strings.ReplaceAll(url, ":", "/"), "/")), ".git")
	if !folderRe.MatchString(n) || n == "." || n == ".." {
		return "src"
	}
	return n
}

var folderRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// inside resolves p and fails if it escapes root (e.g. "dockerfile": "../../etc/passwd").
// Errors show the path relative to the repo, never the host's volume path.
func inside(root, p string) (string, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%s points outside the repo", rel)
	}
	return p, nil
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func podman(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = &stdout
	err := runCmd(cmd)
	return strings.TrimSpace(stdout.String()), err
}

func run(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return runCmd(cmd)
}

// runCmd runs cmd and folds the tail of stderr into the error, so failures are readable in the UI.
func runCmd(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(cmd.Path), cmd.Args[1], err, msg)
	}
	return nil
}
