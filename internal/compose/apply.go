package compose

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/workspace"
)

// Apply syncs the project with its compose file: it creates, updates and removes apps as Plan
// says, saves src as the project's compose source, and deploys every service in depends_on
// order (one deploy at a time, each healthy before the next). Removed apps keep their volumes.
// ponytail: it stops at the first error; the next sync picks up from there.
func (s *Service) Apply(ctx context.Context, project string, src Source) (Diff, error) {
	d, err := s.Plan(ctx, project, src)
	if err != nil {
		return Diff{}, err
	}
	node, err := s.node(ctx, project)
	if err != nil {
		return Diff{}, err
	}
	for _, p := range d.Services {
		if err := s.applyOne(ctx, project, node.Name, p, slices.Contains(d.Create, p.App)); err != nil {
			return d, err
		}
	}
	for _, name := range d.Remove {
		if err := s.apps.Delete(ctx, name, false); err != nil {
			return d, err
		}
	}
	if err := s.save(ctx, project, src); err != nil {
		return d, err
	}
	for _, p := range d.Services {
		dep, err := s.apps.Deploy(ctx, p.App, "")
		if err != nil {
			return d, err
		}
		d.Deployments = append(d.Deployments, dep)
	}
	return d, nil
}

// applyOne creates or updates one service's app: source, volumes, and its compose variables
// (variables added by hand in the UI stay; the compose file's win).
func (s *Service) applyOne(ctx context.Context, project, node string, p Planned, create bool) error {
	if create {
		in := app.CreateInput{Name: p.App, Node: node, Project: project, Compose: p.Service,
			Description: "compose service " + p.Service}
		in.RepoURL, in.Image, in.Branch, in.Dockerfile, in.Port, in.HealthPath = p.Input.RepoURL, p.Input.Image, p.Input.Branch, p.Input.Dockerfile, p.Input.Port, p.Input.HealthPath
		in.Health, in.Command, in.Context = p.Input.Health, p.Input.Command, p.Input.Context
		if _, err := s.apps.Create(ctx, in); err != nil {
			return err
		}
	} else if _, err := s.apps.SetSource(ctx, p.App, p.Input); err != nil {
		return err
	}
	if _, err := s.apps.SetVolumes(ctx, p.App, p.Volumes); err != nil {
		return err
	}
	current, err := s.apps.Env(ctx, p.App)
	if err != nil {
		return err
	}
	return s.apps.SetEnv(ctx, p.App, mergeEnv(current, p.Env))
}

// mergeEnv sets each KEY=VALUE of the compose file over the app's variables, keeping the rest.
func mergeEnv(current []workspace.EnvVar, compose []string) []workspace.EnvVar {
	out := append([]workspace.EnvVar{}, current...)
	for _, kv := range compose {
		k, v, _ := strings.Cut(kv, "=")
		if i := slices.IndexFunc(out, func(e workspace.EnvVar) bool { return e.Key == k }); i >= 0 {
			out[i].Value = v
		} else {
			out = append(out, workspace.EnvVar{Key: k, Value: v})
		}
	}
	return out
}

func (s *Service) save(ctx context.Context, project string, src Source) error {
	id, err := s.projectID(ctx, project)
	if err != nil {
		return err
	}
	if src.RepoURL != "" {
		src.Text = "" // read again from the repo on each sync
	}
	b, _ := json.Marshal(src)
	_, err = s.db.Write.ExecContext(ctx, `UPDATE projects SET compose = ? WHERE id = ?`, string(b), id)
	return err
}
