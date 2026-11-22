package agent

import (
	"errors"
	"net"
	"path"
	"regexp"
	"strings"

	"github.com/jefjesuswt/walroos/shared/names"
)

var ErrInvalidApp = errors.New("invalid app: name must be a DNS label, port 1-65535, sha hex, branch and dockerfile without spaces or a leading '-', health_path a URL path")

const AppNetwork = "walroos" // apps reach each other here by container name; apps publish no host ports

// AppSpec is one deploy. Image set means rollback: run that image, skip clone and build.
// Source set means a published image: pull it instead of cloning and building.
type AppSpec struct {
	Name       string       `json:"name"`
	ID         string       `json:"id"` // deployment id from the hub; the container is <name>-<id>
	RepoURL    string       `json:"repo_url"`
	Source     string       `json:"source,omitempty"` // a published image (registry/repo:tag), instead of RepoURL
	Branch     string       `json:"branch"`
	Sha        string       `json:"sha"`                 // "" deploys the branch head
	Dockerfile string       `json:"dockerfile"`          // relative to the repo; default Dockerfile
	Static     *StaticBuild `json:"static,omitempty"`    // set: no Dockerfile, build and serve it as a static site
	GitToken   string       `json:"git_token,omitempty"` // the hub's GitHub token, for a private github.com repo; only used to clone
	Port       int          `json:"port"`
	HealthPath string       `json:"health_path"` // default /
	Image      string       `json:"image"`
	Env        []string     `json:"env"`
	Volumes    []Volume     `json:"volumes,omitempty"`    // with any, a deploy stops the old release before starting the new one
	Network    string       `json:"network,omitempty"`    // the project's network (ProjectNetwork); "" = AppNetwork
	Pull       string       `json:"pull,omitempty"`       // build node's registry (tailnet host:port): pull Sha from it, don't build
	PullToken  string       `json:"pull_token,omitempty"` // PullToken for this app on that node
}

// Volume is storage that outlives releases: a named podman volume (Name) or a directory
// under the node's data root (HostPath, relative to it). Deleting the app keeps both unless
// it's purged, and then only named volumes go.
type Volume struct {
	Name     string `json:"name,omitempty"`
	HostPath string `json:"host_path,omitempty"`
	Target   string `json:"target"` // absolute path in the container
	RO       bool   `json:"ro,omitempty"`
}

// Release is what a successful deploy left running.
type Release struct {
	Sha       string `json:"sha"`
	Image     string `json:"image"`
	Container string `json:"container"`
}

// What POST /v1/apps/{name}/power does to the running release. A stopped app stays stopped
// after a reboot; a deploy or a rollback starts the new release.
const (
	AppStop    = "stop"
	AppStart   = "start"
	AppRestart = "restart"
)

// App is an app's state on this node: the release that's running, if any.
type App struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	Sha       string `json:"sha"`
	Image     string `json:"image"`
	State     string `json:"state"` // systemd ActiveState: active, activating, failed, inactive (stopped)...
}

var (
	deployIDRe   = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	shaRe        = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	refRe        = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]{0,199}$`)    // branch, dockerfile: no leading "-" (git/podman options)
	healthPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,511}$`) // goes into the raw HTTP request line
	tokenRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	volNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)
	pathRe       = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`) // no ':' or ',': it goes into a Volume= line
	// A fully qualified image: a registry host with a dot (no short-name lookup, no localhost),
	// then a lowercase repo path, an optional tag and an optional digest.
	imageRe = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+(:[0-9]{1,5})?/[a-z0-9]+((\.|_|__|-+|/)[a-z0-9]+)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127})?(@sha256:[0-9a-f]{64})?$`)
)

// Valid reports whether spec is safe to hand to git, podman and the health check. The hub
// checks it too, to answer 400 instead of a failed deployment.
func (spec AppSpec) Valid() bool {
	opt := func(re *regexp.Regexp, v string) bool { return v == "" || re.MatchString(v) }
	ownImage := "localhost/walroos-app-" + spec.Name + ":"
	tag, isOwn := strings.CutPrefix(spec.Image, ownImage)
	return names.DNSLabel(spec.Name) && deployIDRe.MatchString(spec.ID) && spec.Port >= 1 && spec.Port <= 65535 &&
		(spec.RepoURL != "" || spec.Image != "" || spec.Source != "") && !strings.HasPrefix(spec.RepoURL, "-") &&
		(spec.Source == "" || len(spec.Source) <= 255 && imageRe.MatchString(spec.Source) && spec.RepoURL == "" && spec.Pull == "") &&
		opt(shaRe, spec.Sha) && opt(refRe, spec.Branch) && opt(refRe, spec.Dockerfile) && opt(healthPathRe, spec.HealthPath) &&
		(spec.Image == "" || isOwn && len(tag) == 12 && shaRe.MatchString(tag)) && // rollback runs only this app's own builds
		validVolumes(spec.Volumes) && ValidNetwork(spec.Network) &&
		spec.Static.valid() && (spec.Static == nil || spec.Dockerfile == "" && spec.Source == "" && spec.Port == StaticPort) &&
		(spec.Pull == "" || validHostPort(spec.Pull) && tokenRe.MatchString(spec.PullToken) && len(spec.Sha) >= 12 && spec.Image == "")
}

func validVolumes(vols []Volume) bool {
	clean := func(p string, abs bool) bool {
		return pathRe.MatchString(p) && path.Clean(p) == p && path.IsAbs(p) == abs && p != "/" && p != "." && p != ".." && !strings.HasPrefix(p, "../")
	}
	targets := map[string]bool{}
	for _, v := range vols {
		if (v.Name == "") == (v.HostPath == "") || v.Name != "" && !volNameRe.MatchString(v.Name) ||
			v.HostPath != "" && !clean(v.HostPath, false) || !clean(v.Target, true) || targets[v.Target] {
			return false
		}
		targets[v.Target] = true
	}
	return len(vols) <= 16
}

func validHostPort(v string) bool {
	host, port, err := net.SplitHostPort(v)
	return err == nil && host != "" && !strings.ContainsAny(host, "/@") && port != ""
}

// ProjectNetwork is the podman network a project's services share: they reach each other by
// name there and nothing else can. The default project keeps AppNetwork, so services created
// before projects existed don't move.
func ProjectNetwork(project string) string {
	if project == "" || project == "default" {
		return AppNetwork
	}
	return "walroos-p-" + project
}

func ValidNetwork(n string) bool {
	p, ok := strings.CutPrefix(n, "walroos-p-")
	return n == "" || n == AppNetwork || ok && names.DNSLabel(p)
}
