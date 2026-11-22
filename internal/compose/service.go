package compose

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/project/lookup"
	"github.com/jefjesuswt/walroos/shared/envref"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrDefault  = whttp.Invalid("a compose file goes in a project of its own: create one first")
	ErrNoSource = whttp.Invalid("give a repo (and the compose file's path in it) or paste the compose file")
	ErrTaken    = whttp.Conflict("an app with that name already exists outside this project's compose")
	errFetch    = whttp.Invalid("couldn't read the compose file from the repo")
)

// Service syncs a project with its compose file: the services become the project's apps.
type Service struct {
	db     *config.Database
	apps   *app.Service
	nodes  *fleet.Service
	agents *agent.Client
	vars   func(ctx context.Context, projectID string) (envref.Vars, error) // the project's shared variables, for ${VAR}
	token  func(ctx context.Context, repoURL string) string                 // the GitHub token for a private repo
}

func NewService(db *config.Database, apps *app.Service, nodes *fleet.Service, agents *agent.Client,
	vars func(context.Context, string) (envref.Vars, error), token func(context.Context, string) string) *Service {
	return &Service{db: db, apps: apps, nodes: nodes, agents: agents, vars: vars, token: token}
}

// Diff is what a sync does: the apps it creates, updates and removes, in order, and why some keys
// were ignored. After Apply it also has the deployments it queued.
type Diff struct {
	Services    []Planned        `json:"services"`
	Create      []string         `json:"create"`
	Update      []string         `json:"update"`
	Remove      []string         `json:"remove"`
	Warnings    []string         `json:"warnings"`
	Deployments []app.Deployment `json:"deployments,omitempty"`
}

// Source is the project's saved compose source ("" fields when it has none).
func (s *Service) Source(ctx context.Context, project string) (Source, error) {
	id, err := s.projectID(ctx, project)
	if err != nil {
		return Source{}, err
	}
	var raw string
	if err := s.db.Read.QueryRowContext(ctx, `SELECT compose FROM projects WHERE id = ?`, id).Scan(&raw); err != nil || raw == "" {
		return Source{}, err
	}
	var src Source
	return src, json.Unmarshal([]byte(raw), &src)
}

// Plan reads the compose file and says what Apply would do, changing nothing.
func (s *Service) Plan(ctx context.Context, project string, src Source) (Diff, error) {
	id, err := s.projectID(ctx, project)
	if err != nil {
		return Diff{}, err
	}
	file, files, err := s.read(ctx, id, project, src)
	if err != nil {
		return Diff{}, err
	}
	plan, err := Translate(file, project, src, files)
	if err != nil {
		return Diff{}, whttp.Invalid(err.Error())
	}
	return s.diff(ctx, id, plan, file.Warnings)
}

// diff compares plan with the project's compose apps, and checks every service is a valid app.
func (s *Service) diff(ctx context.Context, projectID string, plan []Planned, warnings []string) (Diff, error) {
	apps, err := s.apps.List(ctx)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{Services: plan, Create: []string{}, Update: []string{}, Remove: []string{}, Warnings: append([]string{}, warnings...)}
	for _, p := range plan {
		if err := app.CheckSource(p.App, p.Input); err != nil {
			return Diff{}, whttp.Invalid("services." + p.Service + ": " + err.Error())
		}
		i := slices.IndexFunc(apps, func(a app.App) bool { return a.Name == p.App })
		switch {
		case i < 0:
			d.Create = append(d.Create, p.App)
		case apps[i].ProjectID != projectID || apps[i].Compose != p.Service:
			return Diff{}, whttp.Conflict(p.App + ": " + ErrTaken.Error())
		default:
			d.Update = append(d.Update, p.App)
		}
	}
	for _, a := range apps {
		if a.ProjectID == projectID && a.Compose != "" && !slices.ContainsFunc(plan, func(p Planned) bool { return p.App == a.Name }) {
			d.Remove = append(d.Remove, a.Name)
		}
	}
	return d, nil
}

// read gets the compose text (pasted, or from the repo through a node) and the env_files it
// names, and parses it with the project's shared variables and the repo's .env.
func (s *Service) read(ctx context.Context, projectID, project string, src Source) (*File, map[string]string, error) {
	vars := map[string]string{}
	if v, err := s.vars(ctx, projectID); err == nil {
		vars = v["shared"]
	}
	if src.RepoURL == "" {
		if src.Text == "" {
			return nil, nil, ErrNoSource
		}
		f, err := Parse(src.Text, vars)
		if err != nil {
			return nil, nil, whttp.Invalid(err.Error())
		}
		return f, nil, nil
	}
	dir := path.Dir(src.File())
	files, err := s.fetch(ctx, project, src, src.File(), path.Join(dir, ".env"))
	if err != nil {
		return nil, nil, err
	}
	text, ok := files[src.File()]
	if !ok {
		return nil, nil, whttp.Invalid(src.File() + " isn't in the repo")
	}
	f, err := Parse(text, withDotEnv(vars, files[path.Join(dir, ".env")]))
	if err != nil {
		return nil, nil, whttp.Invalid(err.Error())
	}
	var more []string
	for _, svc := range f.Services {
		for _, e := range svc.EnvFiles {
			if p := path.Clean(path.Join(dir, e)); files[p] == "" {
				more = append(more, p)
			}
		}
	}
	if len(more) > 0 {
		extra, err := s.fetch(ctx, project, src, more...)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range extra {
			files[k] = v
		}
	}
	return f, files, nil
}

// withDotEnv adds the repo's .env under the project's variables, which win (like the shell's in
// docker compose).
func withDotEnv(vars map[string]string, dotenv string) map[string]string {
	out := map[string]string{}
	for _, kv := range envFile(dotenv) {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	for k, v := range vars {
		out[k] = v
	}
	return out
}

// fetch reads paths from the repo on the project's node (or any prod node for a new project).
func (s *Service) fetch(ctx context.Context, project string, src Source, paths ...string) (map[string]string, error) {
	node, err := s.node(ctx, project)
	if err != nil {
		return nil, err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	files, err := s.agents.RepoFiles(ctx, address, tokenHash, agent.RepoFilesRequest{
		RepoURL: src.RepoURL, Branch: src.Branch, GitToken: s.token(ctx, src.RepoURL), Paths: paths})
	if err != nil {
		var ae *agent.AgentError
		if errors.As(err, &ae) {
			return nil, whttp.Invalid(errFetch.Error() + ": " + ae.Msg)
		}
		return nil, err
	}
	return files, nil
}

// node is where the project's services run: one node, so they share its project network. The
// project's first app decides; a new project gets a prod node.
func (s *Service) node(ctx context.Context, project string) (fleet.Node, error) {
	apps, err := s.apps.List(ctx)
	if err != nil {
		return fleet.Node{}, err
	}
	want := ""
	for _, a := range apps {
		if a.Project == project {
			want = a.NodeID
			break
		}
	}
	return s.nodes.Pick(ctx, fleet.Placement{Role: "prod", NoNode: app.ErrNoNode, WrongRole: app.ErrNodeRole}, want, nil)
}

func (s *Service) projectID(ctx context.Context, project string) (string, error) {
	if project == "" || project == "default" {
		return "", ErrDefault
	}
	return lookup.ID(ctx, s.db, project)
}
