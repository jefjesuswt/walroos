package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
)

const deployTimeout = 30 * time.Minute

// deployer owns the deployment queue: it records deployments and runs them one at a time on
// the app's node, built first on a build node when there's one. Service embeds it.
type deployer struct {
	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	env    func(ctx context.Context, appID string) ([]string, error) // KEY=VALUE for the container
	kick   chan struct{}
	now    func() time.Time
}

// Deploy queues a deployment of sha ("" = the branch head; always "" for an image app,
// which pulls its tag again).
func (q *deployer) Deploy(ctx context.Context, name, sha string) (Deployment, error) {
	a, err := q.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	if a.RepoURL == "" && a.Image == "" {
		return Deployment{}, ErrNoSource
	}
	if !(agent.AppSpec{Name: a.Name, ID: "x", RepoURL: a.RepoURL, Source: a.Image, Port: a.Port, Sha: sha}).Valid() || a.Image != "" && sha != "" {
		return Deployment{}, ErrInvalid
	}
	return q.enqueue(ctx, Deployment{AppID: a.ID, Sha: sha})
}

// Rollback queues a deployment that runs the image of a succeeded deployment, with no build.
func (q *deployer) Rollback(ctx context.Context, name, id string) (Deployment, error) {
	a, err := q.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	src, err := q.store.Deployment(ctx, a.ID, id)
	if err != nil {
		return Deployment{}, err
	}
	if src.State != "succeeded" || src.Image == "" {
		return Deployment{}, ErrNoImage
	}
	return q.enqueue(ctx, Deployment{AppID: a.ID, Sha: src.Sha, Image: src.Image})
}

func (q *deployer) enqueue(ctx context.Context, d Deployment) (Deployment, error) {
	d.ID, d.State, d.CreatedAt = randomID(), "queued", q.now().UTC()
	if err := q.store.InsertDeployment(ctx, d); err != nil {
		return Deployment{}, err
	}
	select {
	case q.kick <- struct{}{}:
	default:
	}
	return d, nil
}

func (q *deployer) Deployments(ctx context.Context, name string) ([]Deployment, error) {
	a, err := q.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return q.store.Deployments(ctx, a.ID, 50)
}

func (q *deployer) Deployment(ctx context.Context, name, id string) (Deployment, error) {
	a, err := q.store.Get(ctx, name)
	if err != nil {
		return Deployment{}, err
	}
	return q.store.Deployment(ctx, a.ID, id)
}

// AllDeployments lists the newest deployments of every app, without logs.
func (q *deployer) AllDeployments(ctx context.Context) ([]Deployment, error) {
	return q.store.AllDeployments(ctx, 100)
}

// Run works the queue until ctx ends. ponytail: one deployment at a time hub-wide; per-node
// workers when several prod nodes deploy at once.
func (q *deployer) Run(ctx context.Context) {
	if err := q.store.failRunning(ctx, q.now().UTC()); err != nil {
		slog.Error("deployments: mark stale ones failed", "err", err)
	}
	for {
		q.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-q.kick:
		case <-time.After(time.Minute):
		}
	}
}

// drain runs queued deployments until there are none.
func (q *deployer) drain(ctx context.Context) {
	for ctx.Err() == nil {
		d, ok, err := q.store.nextQueued(ctx)
		if err != nil {
			slog.Error("deployments: read queue", "err", err)
		}
		if !ok {
			return
		}
		q.run(ctx, d)
	}
}

// run takes one deployment from queued to succeeded or failed.
func (q *deployer) run(ctx context.Context, d Deployment) {
	if err := q.store.setRunning(ctx, d.ID); err != nil {
		q.fail(ctx, d, err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()
	rel, err := q.release(ctx, d)
	if err != nil {
		q.fail(ctx, d, err)
		return
	}
	if err := q.store.finish(ctx, d.ID, "succeeded", rel.Sha, rel.Image, "", q.now().UTC()); err != nil {
		slog.Error("deployments: record success", "id", d.ID, "err", err)
	}
}

func (q *deployer) fail(ctx context.Context, d Deployment, err error) {
	slog.Warn("deployment failed", "id", d.ID, "err", err)
	if err := q.store.finish(context.WithoutCancel(ctx), d.ID, "failed", "", "", err.Error(), q.now().UTC()); err != nil {
		slog.Error("deployments: record failure", "id", d.ID, "err", err)
	}
}

// release builds (on a build node if there is one) and starts the deployment on the app's node.
func (q *deployer) release(ctx context.Context, d Deployment) (agent.Release, error) {
	a, err := q.store.getByID(ctx, d.AppID)
	if err != nil {
		return agent.Release{}, err
	}
	address, tokenHash, err := q.nodes.AgentTarget(ctx, a.NodeID)
	if err != nil {
		return agent.Release{}, err
	}
	env, err := q.env(ctx, a.ID)
	if err != nil {
		return agent.Release{}, err
	}
	log := func(line string) { q.store.appendLog(ctx, d.ID, line) }
	spec := agent.AppSpec{
		Name: a.Name, ID: d.ID, RepoURL: a.RepoURL, Source: a.Image, Branch: a.Branch, Sha: d.Sha, Dockerfile: a.Dockerfile, Static: a.Static,
		Port: a.Port, HealthPath: a.HealthPath, Image: d.Image, Volumes: a.Volumes, Network: agent.ProjectNetwork(a.Project),
	}
	if a.Static != nil {
		spec.Env = env // a static site's build reads them (VITE_*, NEXT_PUBLIC_*): send them to the build node too
	}
	if b, ok := q.builder(ctx, a.NodeID); ok && d.Image == "" && a.Image == "" {
		if spec, err = q.buildOn(ctx, b, spec, log); err != nil {
			return agent.Release{}, err
		}
		log("deploying on " + a.NodeName)
	}
	spec.Env = env
	return q.agents.DeployApp(ctx, address, tokenHash, spec, log)
}

// buildOn builds the image on build node b and points spec at b's registry to pull it.
func (q *deployer) buildOn(ctx context.Context, b fleet.Node, spec agent.AppSpec, log func(string)) (agent.AppSpec, error) {
	baddr, bhash, err := q.nodes.AgentTarget(ctx, b.ID)
	if err != nil {
		return spec, err
	}
	log("building on " + b.Name)
	rel, err := q.agents.BuildApp(ctx, baddr, bhash, spec, log) // env only for a static site: the rest is for the running app
	if err != nil {
		return spec, fmt.Errorf("build on %s: %w", b.Name, err)
	}
	host, _, err := net.SplitHostPort(baddr)
	if err != nil {
		host = baddr
	}
	spec.Sha, spec.Pull, spec.PullToken = rel.Sha, net.JoinHostPort(host, agent.RegistryPort), agent.PullToken(bhash, spec.Name)
	return spec, nil
}

// builder returns the online build node that builds for target. None when target has the
// build role itself or there's no build node online: then target builds, with no registry.
func (q *deployer) builder(ctx context.Context, target string) (fleet.Node, bool) {
	nodes, err := q.nodes.List(ctx)
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
