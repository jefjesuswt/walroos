package app

import (
	"cmp"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project/lookup"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/names"
)

// Service keeps the apps and runs their deployments one at a time through the node's agent.
type Service struct {
	*deployer // the deployment queue: Deploy, Rollback, Deployments, Run

	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	box    *secretbox.Box
	now    func() time.Time
	// settings has the Cloudflare token domains need.
	settings *settings.Service
	resolve  Resolver // the project's references, for ${{...}} in env values
}

// placement: apps run on prod nodes.
var placement = fleet.Placement{Role: "prod", NoNode: ErrNoNode, WrongRole: ErrNodeRole}

func NewService(store *Store, nodes *fleet.Service, agents *agent.Client, box *secretbox.Box, st *settings.Service) *Service {
	s := &Service{store: store, nodes: nodes, agents: agents, box: box, settings: st, now: time.Now}
	s.deployer = &deployer{store: store, nodes: nodes, agents: agents, env: s.envForAgent, token: s.cloneToken, kick: make(chan struct{}, 1), now: time.Now}
	return s
}

func (s *Service) Create(ctx context.Context, in CreateInput) (App, error) {
	src := withStaticPort(SourceInput{RepoURL: in.RepoURL, Image: in.Image, Branch: in.Branch, Dockerfile: in.Dockerfile, Static: in.Static, Port: in.Port, HealthPath: in.HealthPath})
	in.Description = strings.TrimSpace(in.Description)
	if !names.DNSLabel(in.Name) || in.Name == "from-template" || len(in.Description) > 500 || src != (SourceInput{}) && !validSource(in.Name, src) {
		return App{}, ErrInvalid
	}
	projectID, err := lookup.ID(ctx, s.store.db, in.Project)
	if err != nil {
		return App{}, err
	}
	node, err := s.nodes.Pick(ctx, placement, in.Node, nil)
	if err != nil {
		return App{}, err
	}
	a := App{
		ID: randomID(), Name: in.Name, NodeID: node.ID, NodeName: node.Name, ProjectID: projectID, Project: cmp.Or(in.Project, "default"), RepoURL: in.RepoURL, Image: in.Image, Branch: in.Branch,
		Dockerfile: in.Dockerfile, Static: src.Static, Port: src.Port, HealthPath: in.HealthPath, Description: in.Description, CreatedAt: s.now().UTC(),
	}
	return a, s.store.Insert(ctx, a)
}

// cloneToken is the GitHub token the node clones a private github.com repo with; "" if none.
func (s *Service) cloneToken(ctx context.Context, repoURL string) string {
	if s.settings == nil {
		return ""
	}
	return s.settings.CloneToken(ctx, repoURL)
}

// validSource: a remote repo or a fully qualified image (not both), and what the agent accepts.
func validSource(name string, in SourceInput) bool {
	spec := agent.AppSpec{Name: name, ID: "x", RepoURL: in.RepoURL, Source: in.Image, Branch: in.Branch, Dockerfile: in.Dockerfile, Static: in.Static, Port: in.Port, HealthPath: in.HealthPath}
	fromRepo := in.Image == "" && workspace.RemoteRepo(in.RepoURL)
	fromImage := in.Image != "" && in.RepoURL == "" && in.Branch == "" && in.Dockerfile == ""
	return (fromRepo || fromImage) && spec.Valid()
}

// withStaticPort: a static site always listens on agent.StaticPort, whatever port was sent.
func withStaticPort(in SourceInput) SourceInput {
	if in.Static != nil {
		in.Port = agent.StaticPort
	}
	return in
}

// SetSource gives an app (a draft or not) what to build or pull; it applies on the next deploy.
func (s *Service) SetSource(ctx context.Context, name string, in SourceInput) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	in.RepoURL, in.Image = strings.TrimSpace(in.RepoURL), strings.TrimSpace(in.Image)
	if in = withStaticPort(in); !validSource(a.Name, in) {
		return App{}, ErrInvalid
	}
	a.RepoURL, a.Image, a.Branch, a.Dockerfile, a.Static, a.Port, a.HealthPath = in.RepoURL, in.Image, in.Branch, in.Dockerfile, in.Static, in.Port, in.HealthPath
	if err := s.store.setSource(ctx, a.ID, a); err != nil {
		return App{}, err
	}
	return s.Get(ctx, name)
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

// Power stops, starts or restarts the app's release on its node. A stopped app stays stopped,
// even after the node reboots, until it's started or deployed again.
func (s *Service) Power(ctx context.Context, name, action string) (App, error) {
	if action != agent.AppStop && action != agent.AppStart && action != agent.AppRestart {
		return App{}, ErrPower
	}
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, a.NodeID)
	if err != nil {
		return App{}, err
	}
	if err := s.agents.PowerApp(ctx, address, tokenHash, name, action); err != nil {
		var ae *agent.AgentError
		switch {
		case errors.As(err, &ae) && ae.Status == 409:
			return App{}, ErrBusy
		case errors.As(err, &ae) && ae.Status == 404:
			return App{}, ErrNotDeployed
		}
		return App{}, err
	}
	return s.Get(ctx, name)
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
