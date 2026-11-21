package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/workspace"
)

const deployTimeout = 30 * time.Minute

// Service keeps the apps and runs their deployments one at a time through the node's agent.
type Service struct {
	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	box    *secretbox.Box
	kick   chan struct{}
	now    func() time.Time

	CloudflareAPI string // "" = Cloudflare's; tests point it at a fake
}

func NewService(store *Store, nodes *fleet.Service, agents *agent.Client, box *secretbox.Box) *Service {
	return &Service{store: store, nodes: nodes, agents: agents, box: box, kick: make(chan struct{}, 1), now: time.Now}
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (s *Service) Create(ctx context.Context, in CreateInput) (App, error) {
	src := SourceInput{RepoURL: in.RepoURL, Image: in.Image, Branch: in.Branch, Dockerfile: in.Dockerfile, Port: in.Port, HealthPath: in.HealthPath}
	in.Description = strings.TrimSpace(in.Description)
	if !nameRe.MatchString(in.Name) || in.Name == "from-template" || len(in.Description) > 500 || src != (SourceInput{}) && !validSource(in.Name, src) {
		return App{}, ErrInvalid
	}
	projectID, err := ProjectID(ctx, s.store.db, in.Project)
	if err != nil {
		return App{}, err
	}
	node, err := s.pickNode(ctx, in.Node)
	if err != nil {
		return App{}, err
	}
	a := App{
		ID: randomID(), Name: in.Name, NodeID: node.ID, NodeName: node.Name, ProjectID: projectID, Project: cmpOr(in.Project, "default"), RepoURL: in.RepoURL, Image: in.Image, Branch: in.Branch,
		Dockerfile: in.Dockerfile, Port: in.Port, HealthPath: in.HealthPath, Description: in.Description, CreatedAt: s.now().UTC(),
	}
	return a, s.store.Insert(ctx, a)
}

// validSource: a remote repo or a fully qualified image (not both), and what the agent accepts.
func validSource(name string, in SourceInput) bool {
	spec := agent.AppSpec{Name: name, ID: "x", RepoURL: in.RepoURL, Source: in.Image, Branch: in.Branch, Dockerfile: in.Dockerfile, Port: in.Port, HealthPath: in.HealthPath}
	fromRepo := in.Image == "" && workspace.RemoteRepo(in.RepoURL)
	fromImage := in.Image != "" && in.RepoURL == "" && in.Branch == "" && in.Dockerfile == ""
	return (fromRepo || fromImage) && spec.Valid()
}

// SetSource gives an app (a draft or not) what to build or pull; it applies on the next deploy.
func (s *Service) SetSource(ctx context.Context, name string, in SourceInput) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	in.RepoURL, in.Image = strings.TrimSpace(in.RepoURL), strings.TrimSpace(in.Image)
	if !validSource(a.Name, in) {
		return App{}, ErrInvalid
	}
	a.RepoURL, a.Image, a.Branch, a.Dockerfile, a.Port, a.HealthPath = in.RepoURL, in.Image, in.Branch, in.Dockerfile, in.Port, in.HealthPath
	if err := s.store.setSource(ctx, a.ID, a); err != nil {
		return App{}, err
	}
	return s.Get(ctx, name)
}

// pickNode returns the requested node (name or id) if it has the prod role, else the first
// online prod node. ponytail: no spreading across prod nodes; pass node when there are several.
func (s *Service) pickNode(ctx context.Context, want string) (fleet.Node, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return fleet.Node{}, err
	}
	for _, n := range nodes {
		if want != "" && (n.ID == want || n.Name == want) {
			if !slices.Contains(n.Roles, "prod") {
				return fleet.Node{}, ErrNodeRole
			}
			return n, nil
		}
		if want == "" && slices.Contains(n.Roles, "prod") && n.Status == "online" {
			return n, nil
		}
	}
	if want != "" {
		return fleet.Node{}, fleet.ErrNodeNotFound
	}
	return fleet.Node{}, ErrNoNode
}

func (s *Service) List(ctx context.Context) ([]App, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	live := s.downNodes(ctx) // per node, asked once; nodes known to be down are never asked
	domains, err := s.store.domains(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		s.withLast(ctx, &list[i], live)
		list[i].Domains = append([]string{}, domains[list[i].ID]...)
	}
	return list, nil
}

func (s *Service) Get(ctx context.Context, name string) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return a, err
	}
	s.withLast(ctx, &a, s.downNodes(ctx))
	domains, err := s.store.domains(ctx)
	a.Domains = append([]string{}, domains[a.ID]...)
	return a, err
}

// downNodes seeds withLast's per-node cache with nil (NodeDown) for nodes whose heartbeats
// stopped, so listing apps doesn't wait out an agent timeout for each of them.
func (s *Service) downNodes(ctx context.Context) map[string][]agent.App {
	live := map[string][]agent.App{}
	if online, err := s.nodes.Online(ctx); err == nil {
		for id, up := range online {
			if !up {
				live[id] = nil
			}
		}
	}
	return live
}

// withLast adds the last deployment and the release the node is running: that one is the
// truth, a deployment's state is only what the hub saw. An unreachable node leaves Running nil
// and NodeDown set. ponytail: one query per app for its last deployment; fine for a handful.
func (s *Service) withLast(ctx context.Context, a *App, live map[string][]agent.App) {
	if d, err := s.store.Deployments(ctx, a.ID, 1); err == nil && len(d) == 1 {
		a.Last = &d[0]
	}
	releases, ok := live[a.NodeID]
	if !ok {
		address, tokenHash, err := s.nodes.AgentTarget(ctx, a.NodeID)
		if err == nil {
			releases, err = s.agents.ListApps(ctx, address, tokenHash)
		}
		if err != nil {
			releases = nil
		}
		live[a.NodeID] = releases
	}
	if releases == nil {
		a.NodeDown = true
		return
	}
	for _, r := range releases {
		if r.Name == a.Name {
			a.Running = &r
		}
	}
}

// AllDeployments lists the newest deployments of every app, without logs.
func (s *Service) AllDeployments(ctx context.Context) ([]Deployment, error) {
	return s.store.AllDeployments(ctx, 100)
}

// SetVolumes replaces the app's volumes; they apply on the next deploy.
func (s *Service) SetVolumes(ctx context.Context, name string, vols []agent.Volume) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	if vols == nil {
		vols = []agent.Volume{}
	}
	// Only the volumes are checked here: a draft has no source or port yet.
	if !(agent.AppSpec{Name: a.Name, ID: "x", RepoURL: "https://example.com/x", Port: 1, Volumes: vols}).Valid() {
		return App{}, ErrVolumes
	}
	if err := s.store.setVolumes(ctx, a.ID, vols); err != nil {
		return App{}, err
	}
	return s.Get(ctx, name)
}

// Delete stops the app on its node and removes it with its history. purge also removes its
// named volumes there; otherwise an app created again with the same name gets them back.
func (s *Service) Delete(ctx context.Context, name string, purge bool) error {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, a.NodeID)
	if err != nil {
		return err
	}
	if err := s.agents.DeleteApp(ctx, address, tokenHash, name, purge); err != nil {
		var ae *agent.AgentError
		if errors.As(err, &ae) && ae.Status == 409 {
			return ErrBusy
		}
		return err
	}
	domains, err := s.store.domains(ctx)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, a.ID); err != nil {
		return err
	}
	s.unroute(ctx, a.NodeID, domains[a.ID])
	return nil
}

// Logs returns the running release's output from the node's journal.
func (s *Service) Logs(ctx context.Context, name string, lines int) (string, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return "", err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, a.NodeID)
	if err != nil {
		return "", err
	}
	logs, err := s.agents.AppLogs(ctx, address, tokenHash, name, lines)
	var ae *agent.AgentError
	if errors.As(err, &ae) && ae.Status == 404 {
		return "", nil // never deployed, or all deploys failed
	}
	return logs, err
}

// Deploy queues a deployment of sha ("" = the branch head; always "" for an image app,
// which pulls its tag again).
func (s *Service) Deploy(ctx context.Context, name, sha string) (Deployment, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	if a.RepoURL == "" && a.Image == "" {
		return Deployment{}, ErrNoSource
	}
	if !(agent.AppSpec{Name: a.Name, ID: "x", RepoURL: a.RepoURL, Source: a.Image, Port: a.Port, Sha: sha}).Valid() || a.Image != "" && sha != "" {
		return Deployment{}, ErrInvalid
	}
	return s.enqueue(ctx, Deployment{AppID: a.ID, Sha: sha})
}

// Rollback queues a deployment that runs the image of a succeeded deployment, with no build.
func (s *Service) Rollback(ctx context.Context, name, id string) (Deployment, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	src, err := s.store.Deployment(ctx, a.ID, id)
	if err != nil {
		return Deployment{}, err
	}
	if src.State != "succeeded" || src.Image == "" {
		return Deployment{}, ErrNoImage
	}
	return s.enqueue(ctx, Deployment{AppID: a.ID, Sha: src.Sha, Image: src.Image})
}

func (s *Service) enqueue(ctx context.Context, d Deployment) (Deployment, error) {
	d.ID, d.State, d.CreatedAt = randomID(), "queued", s.now().UTC()
	if err := s.store.InsertDeployment(ctx, d); err != nil {
		return Deployment{}, err
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return d, nil
}

func (s *Service) Deployments(ctx context.Context, name string) ([]Deployment, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.store.Deployments(ctx, a.ID, 50)
}

func (s *Service) Deployment(ctx context.Context, name, id string) (Deployment, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	return s.store.Deployment(ctx, a.ID, id)
}

// Run works the queue until ctx ends. ponytail: one deployment at a time hub-wide; per-node
// workers when several prod nodes deploy at once.
func (s *Service) Run(ctx context.Context) {
	if err := s.store.failRunning(ctx, s.now().UTC()); err != nil {
		slog.Error("deployments: mark stale ones failed", "err", err)
	}
	for {
		for ctx.Err() == nil {
			d, ok, err := s.store.nextQueued(ctx)
			if err != nil {
				slog.Error("deployments: read queue", "err", err)
			}
			if !ok {
				break
			}
			s.run(ctx, d)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-time.After(time.Minute):
		}
	}
}

func (s *Service) run(ctx context.Context, d Deployment) {
	fail := func(err error) {
		slog.Warn("deployment failed", "id", d.ID, "err", err)
		if err := s.store.finish(context.WithoutCancel(ctx), d.ID, "failed", "", "", err.Error(), s.now().UTC()); err != nil {
			slog.Error("deployments: record failure", "id", d.ID, "err", err)
		}
	}
	if err := s.store.setRunning(ctx, d.ID); err != nil {
		fail(err)
		return
	}
	a, err := s.store.getByID(ctx, d.AppID)
	if err != nil {
		fail(err)
		return
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, a.NodeID)
	if err != nil {
		fail(err)
		return
	}
	env, err := s.envForAgent(ctx, a.ID)
	if err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()
	log := func(line string) { s.store.appendLog(ctx, d.ID, line) }
	spec := agent.AppSpec{
		Name: a.Name, ID: d.ID, RepoURL: a.RepoURL, Source: a.Image, Branch: a.Branch, Sha: d.Sha, Dockerfile: a.Dockerfile,
		Port: a.Port, HealthPath: a.HealthPath, Image: d.Image, Volumes: a.Volumes, Network: agent.ProjectNetwork(a.Project),
	}
	if b, ok := s.builder(ctx, a.NodeID); ok && d.Image == "" && a.Image == "" {
		baddr, bhash, err := s.nodes.AgentTarget(ctx, b.ID)
		if err != nil {
			fail(err)
			return
		}
		log("building on " + b.Name)
		rel, err := s.agents.BuildApp(ctx, baddr, bhash, spec, log) // no env: it's for the running app
		if err != nil {
			fail(fmt.Errorf("build on %s: %w", b.Name, err))
			return
		}
		host, _, err := net.SplitHostPort(baddr)
		if err != nil {
			host = baddr
		}
		spec.Sha, spec.Pull, spec.PullToken = rel.Sha, net.JoinHostPort(host, agent.RegistryPort), agent.PullToken(bhash, a.Name)
		log("deploying on " + a.NodeName)
	}
	spec.Env = env
	rel, err := s.agents.DeployApp(ctx, address, tokenHash, spec, log)
	if err != nil {
		fail(err)
		return
	}
	if err := s.store.finish(ctx, d.ID, "succeeded", rel.Sha, rel.Image, "", s.now().UTC()); err != nil {
		slog.Error("deployments: record success", "id", d.ID, "err", err)
	}
}

// builder returns the online build node that builds for target. None when target has the
// build role itself or there's no build node online: then target builds, with no registry.
func (s *Service) builder(ctx context.Context, target string) (fleet.Node, bool) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return fleet.Node{}, false
	}
	var found *fleet.Node
	for i, n := range nodes {
		if n.ID == target && slices.Contains(n.Roles, "build") {
			return fleet.Node{}, false
		}
		if found == nil && n.ID != target && slices.Contains(n.Roles, "build") && n.Status == "online" {
			found = &nodes[i]
		}
	}
	if found == nil {
		return fleet.Node{}, false
	}
	return *found, true
}

func (s *Service) Env(ctx context.Context, name string) ([]workspace.EnvVar, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := s.store.Env(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	for i := range vars {
		v, err := s.box.Open(vars[i].Value)
		if err != nil {
			v = "" // wrong key or corrupt: don't break the whole list
		}
		vars[i].Value = v
	}
	return vars, nil
}

// SetEnv replaces the variables; they apply on the next deploy.
func (s *Service) SetEnv(ctx context.Context, name string, vars []workspace.EnvVar) error {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	sealed, err := workspace.SealEnv(s.box, vars)
	if err != nil {
		return err
	}
	return s.store.ReplaceEnv(ctx, a.ID, sealed)
}

func (s *Service) envForAgent(ctx context.Context, appID string) ([]string, error) {
	vars, err := s.store.Env(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range vars {
		if val, err := s.box.Open(v.Value); err == nil && val != "" {
			out = append(out, v.Key+"="+val)
		}
	}
	return out, nil
}
