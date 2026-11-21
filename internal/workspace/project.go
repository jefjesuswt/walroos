package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/github"
)

var (
	ErrNoGitHub     = errors.New("GitHub isn't set up: add a token in Settings, or start the project as a local git repo")
	ErrStackVersion = errors.New("unknown stack or version: pick one of GET /api/workspaces/stacks")
)

const (
	githubTokenKey = "github_token" // sealed like env values
	githubLoginKey = "github_login" // who the token is, for the UI; not secret
)

// ProjectInput starts a project that has no repo yet. With GitHub the hub creates the repo on
// the token's account and commits a devcontainer and a README; without it the node runs git init.
type ProjectInput struct {
	Name    string `json:"name"`
	Stack   string `json:"stack"`
	Version string `json:"version"`
	GitHub  bool   `json:"github"`
	Private bool   `json:"private"`
	Node    string `json:"node"`
}

// GitHubStatus is what the UI shows; the token itself never leaves the hub.
type GitHubStatus struct {
	Configured bool   `json:"configured"`
	Login      string `json:"login,omitempty"`
}

// SetGitHub stores the token (sealed) once GitHub says who it is and that it may create repos.
func (s *Service) SetGitHub(ctx context.Context, token string) (GitHubStatus, error) {
	if token == "" {
		return GitHubStatus{}, fmt.Errorf("%w: empty token", ErrInvalid)
	}
	u, err := github.New(token, s.GitHubAPI).User(ctx)
	if err != nil {
		return GitHubStatus{}, err
	}
	if !u.CanCreateRepos() {
		return GitHubStatus{}, github.ErrScope
	}
	sealed, err := s.box.Seal(token)
	if err != nil {
		return GitHubStatus{}, err
	}
	if err := s.store.setSettings(ctx, map[string]string{githubTokenKey: sealed, githubLoginKey: u.Login}); err != nil {
		return GitHubStatus{}, err
	}
	return GitHubStatus{Configured: true, Login: u.Login}, nil
}

func (s *Service) GitHub(ctx context.Context) (GitHubStatus, error) {
	sealed, err := s.store.setting(ctx, githubTokenKey)
	if err != nil || sealed == "" {
		return GitHubStatus{}, err
	}
	login, err := s.store.setting(ctx, githubLoginKey)
	return GitHubStatus{Configured: true, Login: login}, err
}

func (s *Service) DeleteGitHub(ctx context.Context) error {
	return s.store.setSettings(ctx, map[string]string{githubTokenKey: "", githubLoginKey: ""})
}

func (s *Service) github(ctx context.Context) (*github.Client, error) {
	sealed, err := s.store.setting(ctx, githubTokenKey)
	if err != nil {
		return nil, err
	}
	if sealed == "" {
		return nil, ErrNoGitHub
	}
	token, err := s.box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}
	return github.New(token, s.GitHubAPI), nil
}

// CreateProject checks everything it can before touching GitHub (name, stack, node), so a
// failure there leaves no repo behind. With GitHub the workspace then clones the new repo as usual.
// ponytail: if the first commit or the workspace fails after the repo exists, the repo stays (deleting
// needs another scope); the error says so, and From repo picks it up.
func (s *Service) CreateProject(ctx context.Context, owner string, in ProjectInput) (Workspace, error) {
	if !validName(in.Name) {
		return Workspace{}, fmt.Errorf("%w: name must be a DNS label (lowercase letters, digits, '-')", ErrInvalid)
	}
	dc, ok := agent.ProjectDevcontainer(in.Stack, in.Version)
	if !ok {
		return Workspace{}, ErrStackVersion
	}
	if _, err := s.store.Get(ctx, in.Name); err == nil {
		return Workspace{}, ErrExists
	}
	var gh *github.Client
	if in.GitHub {
		var err error
		if gh, err = s.github(ctx); err != nil {
			return Workspace{}, err
		}
	}
	node, err := s.pickNode(ctx, in.Node)
	if err != nil {
		return Workspace{}, err
	}

	files := map[string]string{
		".devcontainer/devcontainer.json": string(dc) + "\n",
		"README.md":                       readme(in),
	}
	create := CreateInput{Name: in.Name, Node: node.ID, Stack: in.Stack}
	if gh == nil {
		return s.create(ctx, owner, create, files)
	}

	repo, err := gh.CreateRepo(ctx, in.Name, in.Private)
	if err != nil {
		return Workspace{}, err
	}
	if err := gh.FirstCommit(ctx, repo, "Start "+in.Name+" with walroos", files); err != nil {
		return Workspace{}, fmt.Errorf("created %s but its first commit failed: %w", repo.HTMLURL, err)
	}
	create.RepoURL, create.Branch = repo.SSHURL, repo.DefaultBranch // the node clones with its SSH key, then https
	w, err := s.create(ctx, owner, create, nil)
	if err != nil {
		return w, fmt.Errorf("created %s, but not the workspace (create it From repo): %w", repo.HTMLURL, err)
	}
	return w, nil
}

func readme(in ProjectInput) string {
	label := in.Stack
	for _, st := range agent.ProjectStacks {
		if st.ID == in.Stack {
			label = st.Label + " " + in.Version
			if st.VersionOf != "" {
				label = st.Label + " on " + st.VersionOf + " " + in.Version
			}
		}
	}
	return "# " + in.Name + "\n\nA " + label + " project. `.devcontainer/devcontainer.json` sets up the environment; " +
		"open it in any devcontainer-aware tool, or in a walroos workspace.\n"
}
