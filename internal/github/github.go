// Package github is the little of GitHub's REST API walroos needs to start a new project: who
// the token is, create a repo, and make its first commit.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

const API = "https://api.github.com"

var (
	ErrToken       = whttp.Invalid("GitHub rejected the token: it's wrong, expired or revoked")
	ErrScope       = whttp.Invalid("the GitHub token can't create repos or push to them: a classic token needs the repo scope (public_repo for public repos only), a fine-grained one Administration and Contents: read and write")
	ErrRepoExists  = whttp.Conflict("a repo with that name already exists on the GitHub account")
	ErrUnavailable = whttp.Upstream("GitHub didn't answer")
)

type Client struct {
	Token string
	URL   string // API, or a fake in tests
	HTTP  *http.Client
}

func New(token, apiURL string) *Client {
	if apiURL == "" {
		apiURL = API
	}
	return &Client{Token: token, URL: apiURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// User is who a token belongs to. Scopes are a classic token's (X-OAuth-Scopes); Classic is false
// for fine-grained tokens, which don't list their permissions.
type User struct {
	Login   string
	Scopes  []string
	Classic bool
}

// CanCreateRepos is false only when GitHub says so: a fine-grained token is tried at creation.
func (u User) CanCreateRepos() bool {
	return !u.Classic || slices.Contains(u.Scopes, "repo") || slices.Contains(u.Scopes, "public_repo")
}

type Repo struct {
	FullName      string `json:"full_name"`
	SSHURL        string `json:"ssh_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
}

// do sends a request and decodes the answer into out. The token only goes in the header, so no
// error here carries it.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL adds nothing and is the same for every call
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, failure(resp, method, path)
	}
	if out == nil {
		return resp.Header, nil
	}
	return resp.Header, json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	rd := bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// failure turns a non-2xx answer into the error that says what to fix.
func failure(resp *http.Response, method, path string) error {
	var e struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	json.NewDecoder(resp.Body).Decode(&e)
	msg := e.Message
	for _, d := range e.Errors {
		msg += "; " + d.Message
	}
	switch {
	case resp.StatusCode == 401:
		return ErrToken
	case resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0",
		resp.StatusCode == 429, resp.StatusCode >= 500:
		return fmt.Errorf("%w (%d %s)", ErrUnavailable, resp.StatusCode, msg)
	case resp.StatusCode == 403, resp.StatusCode == 404: // fine-grained tokens get 404 for what they can't see
		return ErrScope
	case resp.StatusCode == 422 && strings.Contains(msg, "already exists"):
		return ErrRepoExists
	}
	return fmt.Errorf("github: %s %s: %d %s", method, path, resp.StatusCode, msg)
}

func (c *Client) User(ctx context.Context) (User, error) {
	var u struct {
		Login string `json:"login"`
	}
	h, err := c.do(ctx, "GET", "/user", nil, &u)
	if err != nil {
		return User{}, err
	}
	out := User{Login: u.Login}
	if scopes, ok := h["X-Oauth-Scopes"]; ok {
		out.Classic = true
		for _, s := range strings.Split(strings.Join(scopes, ","), ",") {
			if s = strings.TrimSpace(s); s != "" {
				out.Scopes = append(out.Scopes, s)
			}
		}
	}
	return out, nil
}

// CreateRepo creates an empty repo on the token's account.
func (c *Client) CreateRepo(ctx context.Context, name string, private bool) (Repo, error) {
	var r Repo
	_, err := c.do(ctx, "POST", "/user/repos", map[string]any{"name": name, "private": private, "auto_init": false}, &r)
	if err == nil && r.DefaultBranch == "" {
		r.DefaultBranch = "main"
	}
	return r, err
}

// FirstCommit makes one root commit with files on the repo's default branch. An empty repo has
// no git database yet, so the first file goes through the contents API (which creates the branch),
// then a tree with every file replaces that commit.
func (c *Client) FirstCommit(ctx context.Context, r Repo, message string, files map[string]string) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	if len(paths) == 0 {
		return errors.New("github: nothing to commit")
	}
	repo := "/repos/" + r.FullName
	if _, err := c.do(ctx, "PUT", repo+"/contents/"+paths[0], map[string]string{
		"message": message, "content": base64.StdEncoding.EncodeToString([]byte(files[paths[0]])), "branch": r.DefaultBranch,
	}, nil); err != nil {
		return err
	}
	if len(paths) == 1 {
		return nil
	}

	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	var tree []entry
	for _, p := range paths {
		tree = append(tree, entry{p, "100644", "blob", files[p]})
	}
	var t, commit struct {
		SHA string `json:"sha"`
	}
	if _, err := c.do(ctx, "POST", repo+"/git/trees", map[string]any{"tree": tree}, &t); err != nil {
		return err
	}
	if _, err := c.do(ctx, "POST", repo+"/git/commits", map[string]any{"message": message, "tree": t.SHA, "parents": []string{}}, &commit); err != nil {
		return err
	}
	_, err := c.do(ctx, "PATCH", repo+"/git/refs/heads/"+r.DefaultBranch, map[string]any{"sha": commit.SHA, "force": true}, nil)
	return err
}
