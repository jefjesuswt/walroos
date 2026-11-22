package settings

import (
	"context"
	"strings"

	"github.com/jefjesuswt/walroos/internal/github"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var ErrNoGitHub = whttp.Conflict("GitHub isn't set up: add a token in Settings, or start the project as a local git repo")

const (
	githubTokenKey = "github_token" // sealed
	githubLoginKey = "github_login" // who the token is, for the UI; not secret
)

// GitHubStatus is what the UI shows: whether a token is saved and whose it is.
type GitHubStatus struct {
	Configured bool   `json:"configured"`
	Login      string `json:"login,omitempty"`
}

// SetGitHub stores the token (sealed) once GitHub says who it is and that it may create repos.
func (s *Service) SetGitHub(ctx context.Context, token string) (GitHubStatus, error) {
	token, sealed, err := s.seal(token)
	if err != nil {
		return GitHubStatus{}, err
	}
	u, err := github.New(token, s.GitHubAPI).User(ctx)
	if err != nil {
		return GitHubStatus{}, err
	}
	if !u.CanCreateRepos() {
		return GitHubStatus{}, github.ErrScope
	}
	if err := s.set(ctx, map[string]string{githubTokenKey: sealed, githubLoginKey: u.Login}); err != nil {
		return GitHubStatus{}, err
	}
	return GitHubStatus{Configured: true, Login: u.Login}, nil
}

func (s *Service) GitHub(ctx context.Context) (GitHubStatus, error) {
	sealed, err := s.get(ctx, githubTokenKey)
	if err != nil || sealed == "" {
		return GitHubStatus{}, err
	}
	login, err := s.get(ctx, githubLoginKey)
	return GitHubStatus{Configured: true, Login: login}, err
}

func (s *Service) DeleteGitHub(ctx context.Context) error {
	return s.set(ctx, map[string]string{githubTokenKey: "", githubLoginKey: ""})
}

// GitHubClient is a client with the saved token, or ErrNoGitHub.
func (s *Service) GitHubClient(ctx context.Context) (*github.Client, error) {
	token, err := s.token(ctx, githubTokenKey)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, ErrNoGitHub
	}
	return github.New(token, s.GitHubAPI), nil
}

// CloneToken is what a node clones repoURL with: the saved GitHub token for a github.com repo
// (https or SSH form), so private repos clone without a key on the node; "" otherwise.
func (s *Service) CloneToken(ctx context.Context, repoURL string) string {
	if !strings.Contains(repoURL, "github.com/") && !strings.Contains(repoURL, "github.com:") {
		return ""
	}
	token, _ := s.token(ctx, githubTokenKey)
	return token
}
