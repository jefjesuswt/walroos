package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/secretbox"
)

// Service picks a node, calls its agent and keeps the row. It never talks to podman: the node does.
type Service struct {
	store  *Store
	nodes  *fleet.Service
	agents *agent.Client
	box    *secretbox.Box // encrypts env values at rest
	now    func() time.Time
}

func NewService(store *Store, nodes *fleet.Service, agents *agent.Client, box *secretbox.Box) *Service {
	return &Service{store: store, nodes: nodes, agents: agents, box: box, now: time.Now}
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Create picks a dev node, asks its agent to create the workspace and stores the row.
// The agent answers 202; the workspace becomes running (or failed) in the background.
func (s *Service) Create(ctx context.Context, owner string, in CreateInput) (Workspace, error) {
	if !nameRe.MatchString(in.Name) || in.RepoURL == "" {
		return Workspace{}, ErrInvalid
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
		Name: in.Name, RepoURL: in.RepoURL, Branch: in.Branch, DevcontainerPath: in.DevcontainerPath, Devcontainer: in.Devcontainer,
	}, nil)
	if err != nil {
		return Workspace{}, agentErr(err, "create")
	}

	w := Workspace{
		ID: randomID(), Name: in.Name, Owner: owner, NodeID: node.ID, NodeName: node.Name,
		RepoURL: in.RepoURL, Branch: in.Branch, DevcontainerPath: in.DevcontainerPath,
		Image: ws.Image, State: orDefault(ws.State, "creating"), Error: ws.Error, CreatedAt: s.now().UTC(),
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
	node, err := s.pickNode(ctx, in.Node)
	if err != nil {
		return nil, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	d, err := s.agents.Detect(ctx, address, tokenHash, in.RepoURL, in.Branch)
	var ae *agent.AgentError
	if errors.As(err, &ae) && ae.Status == 422 {
		return nil, fmt.Errorf("%w: %s", ErrRepo, ae.Msg)
	}
	return d, err
}

// pickNode returns the requested node (name or id) if it can run workspaces, else the online
// dev node with the fewest workspaces.
func (s *Service) pickNode(ctx context.Context, want string) (fleet.Node, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return fleet.Node{}, err
	}
	dev := func(n fleet.Node) bool { return slices.Contains(n.Roles, "dev") }

	if want != "" {
		for _, n := range nodes {
			if n.ID == want || n.Name == want {
				if !dev(n) {
					return fleet.Node{}, ErrNodeRole
				}
				return n, nil
			}
		}
		return fleet.Node{}, fleet.ErrNodeNotFound
	}

	usage, err := s.store.nodeUsage(ctx)
	if err != nil {
		return fleet.Node{}, err
	}
	var best *fleet.Node
	for i, n := range nodes {
		if !dev(n) || n.Status != "online" {
			continue
		}
		if best == nil || usage[n.ID] < usage[best.ID] {
			best = &nodes[i]
		}
	}
	if best == nil {
		return fleet.Node{}, ErrNoNode
	}
	return *best, nil
}

// List returns the rows, refreshed from their agents so a workspace that finished creating
// (or failed) shows its real state. ponytail: one call per row; fine for one user's handful.
func (s *Service) List(ctx context.Context) ([]Workspace, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if ws, err := s.refresh(ctx, list[i]); err == nil {
			list[i] = ws
		}
	}
	return list, nil
}

func (s *Service) Get(ctx context.Context, name string) (Workspace, error) {
	w, err := s.store.Get(ctx, name)
	if err != nil {
		return w, err
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
	if ws.State == w.State && ws.Image == w.Image && ws.Error == w.Error {
		return w, nil
	}
	if err := s.store.UpdateState(ctx, w.ID, ws.State, ws.Image, ws.Error); err != nil {
		return w, err
	}
	w.State, w.Image, w.Error = ws.State, ws.Image, ws.Error
	return w, nil
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
	if len(vars) > 64 {
		return fmt.Errorf("%w: at most 64 variables", ErrInvalid)
	}
	seen := map[string]bool{}
	sealed := make([]EnvVar, 0, len(vars))
	for _, v := range vars {
		if !envKeyRe.MatchString(v.Key) || seen[v.Key] {
			return fmt.Errorf("%w: %q is not a valid or unique variable name (A-Z, 0-9, _)", ErrInvalid, v.Key)
		}
		if len(v.Value) > 32<<10 {
			return fmt.Errorf("%w: %s is too long", ErrInvalid, v.Key)
		}
		seen[v.Key] = true
		enc, err := s.box.Seal(v.Value)
		if err != nil {
			return err
		}
		sealed = append(sealed, EnvVar{Key: v.Key, Value: enc, Secret: v.Secret})
	}
	return s.store.ReplaceEnv(ctx, w.ID, sealed)
}

var envKeyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// envList decrypts for the API and for passing KEY=VALUE to the agent.
func (s *Service) envList(ctx context.Context, id string) ([]EnvVar, error) {
	stored, err := s.store.Env(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range stored {
		if v, err := s.box.Open(stored[i].Value); err == nil {
			stored[i].Value = v
		} else {
			stored[i].Value = "" // wrong key or corrupt: don't break the whole list
		}
	}
	return stored, nil
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
