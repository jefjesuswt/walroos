package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/envstore"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/shared/names"
)

// Service picks a node, calls its agent and keeps the row. It never talks to podman: the node does.
type Service struct {
	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	box    *secretbox.Box // encrypts env values at rest
	now    func() time.Time
	// settings has the GitHub token new projects' repos need.
	settings *settings.Service
}

// placement: workspaces run on dev nodes, a new one on the least busy.
var placement = fleet.Placement{Role: "dev", NoNode: ErrNoNode, WrongRole: ErrNodeRole}

// cloneToken is the GitHub token the node clones a private github.com repo with; "" if none.
func (s *Service) cloneToken(ctx context.Context, repoURL string) string {
	if s.settings == nil {
		return ""
	}
	return s.settings.CloneToken(ctx, repoURL)
}

// pickNode places a workspace: the node asked for, else the online dev node with the fewest.
func (s *Service) pickNode(ctx context.Context, want string) (fleet.Node, error) {
	if want != "" {
		return s.nodes.Pick(ctx, placement, want, nil)
	}
	usage, err := s.store.nodeUsage(ctx)
	if err != nil {
		return fleet.Node{}, err
	}
	return s.nodes.Pick(ctx, placement, want, usage)
}

func NewService(store *Store, nodes *fleet.Service, agents *agent.Client, box *secretbox.Box, st *settings.Service) *Service {
	return &Service{store: store, nodes: nodes, agents: agents, box: box, settings: st, now: time.Now}
}

// validName: a DNS label that isn't a route under /api/workspaces.
func validName(n string) bool {
	return names.DNSLabel(n) && n != "stacks" && n != "from-scratch" && n != "detect"
}

// Create picks a dev node, asks its agent to create the workspace and stores the row.
// The agent answers 202; the workspace becomes running (or failed) in the background.
func (s *Service) Create(ctx context.Context, owner string, in CreateInput) (Workspace, error) {
	if !validName(in.Name) || in.RepoURL == "" {
		return Workspace{}, ErrInvalid
	}
	if !RemoteRepo(in.RepoURL) {
		return Workspace{}, ErrRepoURL
	}
	return s.create(ctx, owner, in, nil)
}

// create is Create once the input is checked; init != nil starts a new project with those files
// (git init on the node) instead of cloning in.RepoURL.
func (s *Service) create(ctx context.Context, owner string, in CreateInput, init map[string]string) (Workspace, error) {
	stack, err := normalizeStack(in.Stack)
	if err != nil {
		return Workspace{}, err
	}
	// Before the agent: a name taken on another node would only fail at the insert, after the
	// agent started creating it, and a workspace mid-create can't be deleted.
	if _, err := s.store.Get(ctx, in.Name); err == nil {
		return Workspace{}, ErrExists
	}

	node, err := s.pickNode(ctx, in.Node)
	if err != nil {
		return Workspace{}, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, node.ID)
	if err != nil {
		return Workspace{}, err
	}

	ws, err := s.agents.CreateWorkspace(ctx, address, tokenHash, agent.WorkspaceSpec{
		Name: in.Name, RepoURL: in.RepoURL, Branch: in.Branch, DevcontainerPath: in.DevcontainerPath, Devcontainer: in.Devcontainer, Init: init,
		GitToken: s.cloneToken(ctx, in.RepoURL),
	}, nil)
	if err != nil {
		return Workspace{}, agentErr(err, "create")
	}

	w := Workspace{
		ID: randomID(), Name: in.Name, Owner: owner, NodeID: node.ID, NodeName: node.Name, NodeAddress: node.Address,
		RepoURL: in.RepoURL, Branch: in.Branch, DevcontainerPath: in.DevcontainerPath,
		Image: ws.Image, Stack: stack, State: orDefault(ws.State, "creating"), Error: ws.Error, CreatedAt: s.now().UTC(),
	}
	if err := s.store.Insert(ctx, w); err != nil {
		// The node has it but the hub can't record it: remove it rather than leave an orphan.
		if derr := s.agents.DeleteWorkspace(ctx, address, tokenHash, in.Name); derr != nil {
			slog.Error("workspace created but not recorded, and cleaning up failed", "workspace", in.Name, "node", node.Name, "err", derr)
		}
		return Workspace{}, err
	}
	return w, nil
}

// Detect asks a dev node (the same one Create would pick) to clone the repo shallowly and
// propose a devcontainer. Nothing is stored: the answer goes to the UI as the agent gave it.
func (s *Service) Detect(ctx context.Context, in DetectInput) (json.RawMessage, error) {
	if in.RepoURL == "" {
		return nil, fmt.Errorf("%w: repo_url is required", ErrInvalid)
	}
	if !RemoteRepo(in.RepoURL) {
		return nil, ErrRepoURL
	}
	node, err := s.pickNode(ctx, in.Node)
	if err != nil {
		return nil, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	d, err := s.agents.Detect(ctx, address, tokenHash, in.RepoURL, in.Branch, s.cloneToken(ctx, in.RepoURL))
	var ae *agent.AgentError
	if errors.As(err, &ae) && ae.Status == 422 {
		return nil, fmt.Errorf("%w: %s", ErrRepo, ae.Msg)
	}
	return d, err
}

// RemoteRepo reports whether u is a git URL on another machine. The node's git clones it, so a
// local path or file:// would hand a hub user the node's own disk; ext:: runs a command.
func RemoteRepo(u string) bool {
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(strings.ToLower(u), scheme) && len(u) > len(scheme) {
			return true
		}
	}
	// scp-like user@host:path, as git reads it: no "/" before the ":".
	user, rest, ok := strings.Cut(u, "@")
	host, path, ok2 := strings.Cut(rest, ":")
	return ok && ok2 && user != "" && host != "" && path != "" && !strings.ContainsAny(user+host, `/\ `) && !strings.HasPrefix(u, "-")
}

// List returns the rows, refreshed from their agents so a workspace that finished creating
// (or failed) shows its real state. A node that fails once isn't asked again, or N rows on a
// dead node would take N timeouts. ponytail: one call per row; fine for one user's handful.
func (s *Service) List(ctx context.Context) ([]Workspace, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	// A node that stopped sending heartbeats isn't called at all: its rows show as last known.
	// One that's "online" but doesn't answer is skipped after its first failure.
	down := map[string]bool{}
	if online, err := s.nodes.Online(ctx); err == nil {
		for _, w := range list {
			down[w.NodeID] = !online[w.NodeID]
		}
	}
	for i := range list {
		if down[list[i].NodeID] {
			continue
		}
		ws, err := s.refresh(ctx, list[i])
		var ae *agent.AgentError
		switch {
		case err == nil:
			list[i] = ws
		case !errors.As(err, &ae): // unreachable, not an answer about this workspace
			down[list[i].NodeID] = true
		}
	}
	return list, nil
}

func (s *Service) Get(ctx context.Context, name string) (Workspace, error) {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return w, err
	}
	if online, err := s.nodes.Online(ctx); err == nil && !online[w.NodeID] {
		return w, nil // node down: last known row, no agent timeout
	}
	if fresh, err := s.refresh(ctx, w); err == nil {
		return fresh, nil
	}
	return w, nil // agent unreachable: the last known row is still worth showing
}

// refresh asks the node for the live state and stores it when it changed.
func (s *Service) refresh(ctx context.Context, w Workspace) (Workspace, error) {
	address, tokenHash, err := s.nodes.AgentTarget(ctx, w.NodeID)
	if err != nil {
		return w, err
	}
	ws, err := s.agents.GetWorkspace(ctx, address, tokenHash, w.Name)
	if err != nil {
		return w, err
	}
	w.Folder = ws.Folder // live only: editors open it (zed ssh://host/<folder>)
	if ws.State == w.State && ws.Image == w.Image && ws.Error == w.Error {
		return w, nil
	}
	if err := s.store.UpdateState(ctx, w.ID, ws.State, ws.Image, ws.Error); err != nil {
		return w, err
	}
	w.State, w.Image, w.Error = ws.State, ws.Image, ws.Error
	return w, nil
}

// Authorize returns auth.ErrForbidden unless u may act on the workspace (or ErrNotFound).
func (s *Service) Authorize(ctx context.Context, u auth.User, name string) error {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	return u.CanUseWorkspace(w.Owner)
}

func (s *Service) Start(ctx context.Context, name string) (Workspace, error) {
	return s.op(ctx, name, s.agents.StartWorkspace)
}

func (s *Service) Stop(ctx context.Context, name string) (Workspace, error) {
	return s.op(ctx, name, s.agents.StopWorkspace)
}

// Rebuild recreates the container on the node, keeping the clone and $HOME. The env vars are
// re-sent because the agent deliberately doesn't store them.
func (s *Service) Rebuild(ctx context.Context, name string) (Workspace, error) {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return w, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, w.NodeID)
	if err != nil {
		return w, err
	}
	env, err := s.envForAgent(ctx, w.ID)
	if err != nil {
		return w, err
	}
	ws, err := s.agents.RebuildWorkspace(ctx, address, tokenHash, name, env)
	if err != nil {
		return w, agentErr(err, name)
	}
	if err := s.store.UpdateState(ctx, w.ID, ws.State, ws.Image, ws.Error); err != nil {
		return w, err
	}
	w.State, w.Image, w.Error = ws.State, ws.Image, ws.Error
	return w, nil
}

// Env returns a workspace's variables with their values decrypted (single-user hub; the UI edits them).
func (s *Service) Env(ctx context.Context, name string) ([]EnvVar, error) {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.envList(ctx, w.ID)
}

// SetEnv validates and replaces all variables, encrypting the values.
func (s *Service) SetEnv(ctx context.Context, name string, vars []EnvVar) error {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	sealed, err := envstore.Seal(s.box, vars)
	if err != nil {
		return err
	}
	return envstore.Workspaces.Replace(ctx, s.store.db, w.ID, sealed)
}

// envList decrypts for the API and for passing KEY=VALUE to the agent.
func (s *Service) envList(ctx context.Context, id string) ([]EnvVar, error) {
	stored, err := envstore.Workspaces.Read(ctx, s.store.db, id)
	if err != nil {
		return nil, err
	}
	return envstore.Open(s.box, stored), nil
}

func (s *Service) envForAgent(ctx context.Context, id string) ([]string, error) {
	vars, err := s.envList(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		if v.Value != "" {
			out = append(out, v.Key+"="+v.Value)
		}
	}
	return out, nil
}

func (s *Service) op(ctx context.Context, name string,
	call func(context.Context, string, string, string) (agent.Workspace, error)) (Workspace, error) {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return w, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, w.NodeID)
	if err != nil {
		return w, err
	}
	ws, err := call(ctx, address, tokenHash, name)
	if err != nil {
		return w, agentErr(err, name)
	}
	if err := s.store.UpdateState(ctx, w.ID, ws.State, ws.Image, ws.Error); err != nil {
		return w, err
	}
	w.State, w.Image, w.Error = ws.State, ws.Image, ws.Error
	return w, nil
}

// Delete removes the container and volumes on the node, then the row. A workspace the agent
// doesn't know (already deleted there) is removed from the hub too.
func (s *Service) Delete(ctx context.Context, name string) error {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, w.NodeID)
	if err != nil {
		return err
	}
	if err := s.agents.DeleteWorkspace(ctx, address, tokenHash, name); err != nil {
		var ae *agent.AgentError
		if !errors.As(err, &ae) || ae.Status != 404 {
			return agentErr(err, name)
		}
	}
	return s.store.Delete(ctx, w.ID)
}

// agentErr turns an agent's answer into an error the handlers can map to a status.
func agentErr(err error, what string) error {
	var ae *agent.AgentError
	if errors.As(err, &ae) {
		switch ae.Status {
		case 400:
			return ErrInvalid
		case 404:
			return ErrNotFound
		case 409:
			if ae.Msg == "workspace already exists on this node" {
				return ErrExists
			}
			return ErrAgentBusy
		}
	}
	return err
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
