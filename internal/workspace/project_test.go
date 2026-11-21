package workspace_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/internal/workspace"
)

// fakeGitHub answers the few REST calls a new project makes. Tokens: "classic" (repo scope),
// "gist" (classic without repo), "fine" (fine-grained, may do anything), "fine-ro" (fine-grained
// that can't create repos); anything else is rejected.
type fakeGitHub struct {
	mu      sync.Mutex
	down    bool
	revoked string            // a token that worked when saved and is now refused
	repos   map[string]bool   // names on the account
	private map[string]bool   // name → private, as asked
	files   map[string]string // the tree of the last commit
	commits int               // root commits made through git/commits
	tokens  []string          // every Authorization header, to check none leaks elsewhere
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	g := &fakeGitHub{repos: map[string]bool{"taken": true}, private: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if g.down {
		reply(503, map[string]string{"message": "Service Unavailable"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	g.tokens = append(g.tokens, token)
	switch token {
	case g.revoked:
		reply(401, map[string]string{"message": "Bad credentials"})
		return
	case "classic":
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
	case "gist":
		w.Header().Set("X-OAuth-Scopes", "gist")
	case "fine", "fine-ro":
	default:
		reply(401, map[string]string{"message": "Bad credentials"})
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	repo := strings.TrimPrefix(r.URL.Path, "/repos/me/")
	repo, rest, _ := strings.Cut(repo, "/")

	switch {
	case r.Method == "GET" && r.URL.Path == "/user":
		reply(200, map[string]string{"login": "me"})
	case r.Method == "POST" && r.URL.Path == "/user/repos":
		if token == "fine-ro" {
			reply(403, map[string]string{"message": "Resource not accessible by personal access token"})
			return
		}
		name := body["name"].(string)
		if g.repos[name] {
			reply(422, map[string]any{"message": "Repository creation failed.",
				"errors": []map[string]string{{"resource": "Repository", "field": "name", "message": "name already exists on this account"}}})
			return
		}
		g.repos[name], g.private[name] = true, body["private"].(bool)
		reply(201, map[string]any{"full_name": "me/" + name, "ssh_url": "git@github.com:me/" + name + ".git",
			"html_url": "https://github.com/me/" + name, "default_branch": "main"})
	case !g.repos[repo]:
		reply(404, map[string]string{"message": "Not Found"})
	case r.Method == "PUT" && strings.HasPrefix(rest, "contents/"):
		b, _ := base64.StdEncoding.DecodeString(body["content"].(string))
		g.files = map[string]string{strings.TrimPrefix(rest, "contents/"): string(b)}
		reply(201, map[string]any{"commit": map[string]string{"sha": "c1"}})
	case r.Method == "POST" && rest == "git/trees":
		g.files = map[string]string{}
		for _, e := range body["tree"].([]any) {
			e := e.(map[string]any)
			g.files[e["path"].(string)] = e["content"].(string)
		}
		reply(201, map[string]string{"sha": "t1"})
	case r.Method == "POST" && rest == "git/commits":
		if body["tree"] != "t1" || len(body["parents"].([]any)) != 0 {
			reply(422, map[string]string{"message": "bad commit"})
			return
		}
		g.commits++
		reply(201, map[string]string{"sha": "c2"})
	case r.Method == "PATCH" && rest == "git/refs/heads/main":
		reply(200, map[string]any{"object": map[string]string{"sha": body["sha"].(string)}})
	default:
		reply(404, map[string]string{"message": "Not Found"})
	}
}

func newProjectHub(t *testing.T, withDevNode bool) (*hub, *fakeGitHub, *httptest.Server) {
	h := newHub(t, withDevNode)
	g, srv := newFakeGitHub(t)
	h.set.GitHubAPI = srv.URL
	return h, g, srv
}

func TestProjectStacks(t *testing.T) {
	h, _, _ := newProjectHub(t, true)
	var stacks []agent.ProjectStack
	if code := h.call(t, "GET", "/api/workspaces/stacks", nil, &stacks); code != 200 {
		t.Fatalf("stacks: %d", code)
	}
	ids := map[string][]string{}
	for _, s := range stacks {
		ids[s.ID] = s.Versions
	}
	for _, want := range []string{"go", "node", "bun", "python", "dotnet"} {
		if len(ids[want]) == 0 {
			t.Errorf("stack %s missing or without versions: %v", want, ids)
		}
	}
}

func TestGitHubToken(t *testing.T) {
	h, g, _ := newProjectHub(t, true)

	var st settings.GitHubStatus
	if h.call(t, "GET", "/api/settings/github", nil, &st); st.Configured {
		t.Fatalf("fresh hub: %+v", st)
	}
	for _, c := range []struct {
		token, msg string
		code       int
	}{
		{"", "token is empty", 400},
		{"wrong", "rejected the token", 400},
		{"gist", "repo scope", 400},
	} {
		code, msg := h.callErr(t, "PUT", "/api/settings/github", map[string]string{"token": c.token})
		if code != c.code || !strings.Contains(msg, c.msg) || strings.Contains(msg, c.token+"\"") {
			t.Errorf("PUT %q: %d %q, want %d %q", c.token, code, msg, c.code, c.msg)
		}
	}
	g.down = true
	if code, msg := h.callErr(t, "PUT", "/api/settings/github", map[string]string{"token": "classic"}); code != 502 || !strings.Contains(msg, "didn't answer") {
		t.Errorf("GitHub down: %d %q", code, msg)
	}
	g.down = false
	if h.call(t, "GET", "/api/settings/github", nil, &st); st.Configured {
		t.Fatalf("a refused token was stored: %+v", st)
	}

	for _, token := range []string{"classic", "fine"} {
		if code := h.call(t, "PUT", "/api/settings/github", map[string]string{"token": token}, &st); code != 200 || st.Login != "me" {
			t.Fatalf("PUT %s: %d %+v", token, code, st)
		}
	}
	var sealed string
	h.db.Read.QueryRow(`SELECT value FROM settings WHERE key = 'github_token'`).Scan(&sealed)
	if sealed == "" || strings.Contains(sealed, "fine") {
		t.Errorf("token stored as %q, want it sealed", sealed)
	}
	if h.call(t, "GET", "/api/settings/github", nil, &st); !st.Configured || st.Login != "me" {
		t.Errorf("GET: %+v", st)
	}

	if code := h.call(t, "DELETE", "/api/settings/github", nil, nil); code != 204 {
		t.Fatalf("DELETE: %d", code)
	}
	if h.call(t, "GET", "/api/settings/github", nil, &st); st.Configured {
		t.Errorf("after DELETE: %+v", st)
	}
}

func TestCreateProjectOnGitHub(t *testing.T) {
	h, g, _ := newProjectHub(t, true)
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "classic"}, nil)

	var w workspace.Workspace
	code := h.call(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "shiny", "stack": "go", "version": "1.26", "github": true, "private": true}, &w)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	if w.RepoURL != "git@github.com:me/shiny.git" || w.Branch != "main" || w.Stack != "go" || w.State != "creating" {
		t.Errorf("workspace = %+v", w)
	}
	if !g.private["shiny"] {
		t.Error("repo not created private")
	}
	// One root commit with both files; the workspace clones it like any repo.
	if g.commits != 1 || !strings.Contains(g.files[".devcontainer/devcontainer.json"], `"image": "mcr.microsoft.com/devcontainers/go:1.26"`) ||
		!strings.HasPrefix(g.files["README.md"], "# shiny") {
		t.Errorf("commits = %d, files = %v", g.commits, g.files)
	}
	if h.agent.last.RepoURL != w.RepoURL || h.agent.last.Branch != "main" || h.agent.last.Init != nil {
		t.Errorf("agent got %+v", h.agent.last)
	}
	for _, tok := range g.tokens {
		if tok != "classic" {
			t.Errorf("GitHub got token %q", tok)
		}
	}

	// Bun runs on a Node image and installs bun after the container starts.
	if code := h.call(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "bunny", "stack": "bun", "version": "24", "github": true}, &w); code != 201 {
		t.Fatalf("bun: %d", code)
	}
	if dc := g.files[".devcontainer/devcontainer.json"]; !strings.Contains(dc, "javascript-node:24") || !strings.Contains(dc, "npm install -g bun") || g.private["bunny"] {
		t.Errorf("bun devcontainer = %s, private = %v", dc, g.private["bunny"])
	}
}

func TestCreateProjectLocal(t *testing.T) {
	h, g, _ := newProjectHub(t, true)

	// No token: github=true is refused, github=false is a local git init.
	if code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "solo", "stack": "python", "version": "3.13", "github": true}); code != 409 || !strings.Contains(msg, "GitHub isn't set up") {
		t.Fatalf("no token: %d %q", code, msg)
	}
	var w workspace.Workspace
	if code := h.call(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "solo", "stack": "python", "version": "3.13"}, &w); code != 201 {
		t.Fatalf("local: %d", code)
	}
	if w.RepoURL != "" || w.Stack != "python" {
		t.Errorf("workspace = %+v", w)
	}
	last := h.agent.last
	if last.RepoURL != "" || !strings.Contains(last.Init[".devcontainer/devcontainer.json"], "devcontainers/python:3.13") || last.Init["README.md"] == "" {
		t.Errorf("agent got %+v", last)
	}
	if len(g.tokens) != 0 {
		t.Errorf("GitHub was called %d times for a local project", len(g.tokens))
	}
	// The row lists and refreshes like any workspace.
	var got workspace.Workspace
	if code := h.call(t, "GET", "/api/workspaces/solo", nil, &got); code != 200 || got.Name != "solo" {
		t.Errorf("get: %d %+v", code, got)
	}
}

func TestCreateProjectSad(t *testing.T) {
	h, g, srv := newProjectHub(t, true)
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "classic"}, nil)
	h.call(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "dup", "stack": "go", "version": "1.26"}, nil)

	for _, c := range []struct {
		name string
		body map[string]any
		code int
		msg  string
	}{
		{"bad name", map[string]any{"name": "Bad_Name", "stack": "go", "version": "1.26", "github": true}, 400, "DNS label"},
		{"unknown stack", map[string]any{"name": "a", "stack": "cobol", "version": "1", "github": true}, 400, "unknown stack or version"},
		{"unknown version", map[string]any{"name": "a", "stack": "go", "version": "0.9", "github": true}, 400, "unknown stack or version"},
		{"no version", map[string]any{"name": "a", "stack": "go", "github": true}, 400, "unknown stack or version"},
		{"workspace exists", map[string]any{"name": "dup", "stack": "go", "version": "1.26", "github": true}, 409, "already exists"},
		{"repo exists", map[string]any{"name": "taken", "stack": "go", "version": "1.26", "github": true}, 409, "already exists on the GitHub account"},
	} {
		code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", c.body)
		if code != c.code || !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %d %q, want %d %q", c.name, code, msg, c.code, c.msg)
		}
	}
	if len(g.repos) != 1 {
		t.Errorf("repos = %v: a refused project created one", g.repos)
	}

	// A token that can't create repos (fine-grained without Administration).
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "fine-ro"}, nil)
	if code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "nope", "stack": "go", "version": "1.26", "github": true}); code != 400 || !strings.Contains(msg, "can't create repos") {
		t.Errorf("no scope: %d %q", code, msg)
	}
	// The stored token was revoked since it was saved.
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "fine"}, nil)
	g.revoked = "fine"
	if code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "nope", "stack": "go", "version": "1.26", "github": true}); code != 400 || !strings.Contains(msg, "rejected the token") {
		t.Errorf("revoked token: %d %q", code, msg)
	}
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "classic"}, nil)

	// GitHub down: 5xx, then unreachable. Nothing is created on the node.
	g.down = true
	if code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "nope", "stack": "go", "version": "1.26", "github": true}); code != 502 || !strings.Contains(msg, "didn't answer") {
		t.Errorf("GitHub 503: %d %q", code, msg)
	}
	srv.Close()
	code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "nope", "stack": "go", "version": "1.26", "github": true})
	if code != 502 || !strings.Contains(msg, "didn't answer") || strings.Contains(msg, "classic") {
		t.Errorf("GitHub unreachable: %d %q", code, msg)
	}
	if _, ok := h.agent.ws["nope"]; ok {
		t.Error("the node got a workspace for a project that failed on GitHub")
	}
}

// With no dev node the project fails before GitHub: no repo is left behind.
func TestCreateProjectNoNode(t *testing.T) {
	h, g, _ := newProjectHub(t, false)
	h.call(t, "PUT", "/api/settings/github", map[string]string{"token": "classic"}, nil)
	if code, msg := h.callErr(t, "POST", "/api/workspaces/from-scratch", map[string]any{"name": "lonely", "stack": "go", "version": "1.26", "github": true}); code != 409 || !strings.Contains(msg, "no online node") {
		t.Errorf("no node: %d %q", code, msg)
	}
	if g.repos["lonely"] {
		t.Error("repo created although no node could run the workspace")
	}
}
