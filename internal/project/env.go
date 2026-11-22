package project

import (
	"context"
	"maps"
	"slices"

	"github.com/jefjesuswt/walroos/internal/envstore"
	"github.com/jefjesuswt/walroos/shared/envref"
)

// Shared variables belong to the project. An app uses one by writing ${{shared.KEY}} in its own
// environment, so nothing reaches an app that didn't ask for it.
const shared = "shared"

// Env returns the project's shared variables, opened.
func (s *Service) Env(ctx context.Context, name string) ([]envstore.Var, error) {
	id, err := s.id(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := envstore.Projects.Read(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return envstore.Open(s.box, vars), nil
}

// SetEnv replaces the shared variables; apps get them on their next deploy.
func (s *Service) SetEnv(ctx context.Context, name string, vars []envstore.Var) error {
	id, err := s.id(ctx, name)
	if err != nil {
		return err
	}
	sealed, err := envstore.Seal(s.box, vars)
	if err != nil {
		return err
	}
	return envstore.Projects.Replace(ctx, s.db, id, sealed)
}

// Vars are what references in the project's app environments can name: each app's and each
// database's variables, and the shared ones as "shared". The hub hands this to app.SetResolver.
func (s *Service) Vars(ctx context.Context, projectID string) (envref.Vars, error) {
	vars, err := s.apps.RefVars(ctx, projectID)
	if err != nil {
		return nil, err
	}
	dbs, err := s.dbs.RefVars(ctx, projectID)
	if err != nil {
		return nil, err
	}
	maps.Copy(vars, dbs)
	env, err := envstore.Projects.Read(ctx, s.db, projectID)
	if err != nil {
		return nil, err
	}
	vars[shared] = map[string]string{}
	for _, v := range envstore.Open(s.box, env) {
		vars[shared][v.Key] = v.Value
	}
	return vars, nil
}

// Ref is a service the project's apps can reference, and the variable names it offers. No
// values: the UI shows what to write, not the secrets.
type Ref struct {
	Service string   `json:"service"`
	Keys    []string `json:"keys"`
}

// Refs lists what ${{service.KEY}} can name in the project, services first, shared last.
func (s *Service) Refs(ctx context.Context, name string) ([]Ref, error) {
	id, err := s.id(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := s.Vars(ctx, id)
	if err != nil {
		return nil, err
	}
	refs := []Ref{}
	for _, svc := range slices.Sorted(maps.Keys(vars)) {
		if svc != shared {
			refs = append(refs, Ref{svc, keys(vars[svc])})
		}
	}
	return append(refs, Ref{shared, keys(vars[shared])}), nil
}

// keys are m's keys, sorted; [] for none, never null: the UI reads keys.length.
func keys(m map[string]string) []string {
	return append([]string{}, slices.Sorted(maps.Keys(m))...)
}
