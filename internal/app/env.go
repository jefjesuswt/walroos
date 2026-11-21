package app

import (
	"context"
	"fmt"

	"github.com/jefjesuswt/walroos/internal/envstore"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/envref"
)

// An app's variables are sealed in the hub and handed to its container on each deploy.

func (s *Service) Env(ctx context.Context, name string) ([]workspace.EnvVar, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := envstore.Apps.Read(ctx, s.store.db, a.ID)
	if err != nil {
		return nil, err
	}
	return envstore.Open(s.box, vars), nil
}

// SetEnv replaces the variables; they apply on the next deploy.
func (s *Service) SetEnv(ctx context.Context, name string, vars []workspace.EnvVar) error {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := s.checkRefs(ctx, a.ProjectID, vars); err != nil {
		return err
	}
	sealed, err := envstore.Seal(s.box, vars)
	if err != nil {
		return err
	}
	return envstore.Apps.Replace(ctx, s.store.db, a.ID, sealed)
}

// envForAgent is the app's KEY=VALUE list for its container, references expanded with what the
// project has now: a deploy fails if one names something that's gone.
func (s *Service) envForAgent(ctx context.Context, appID string) ([]string, error) {
	a, err := s.store.getByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	vars, err := envstore.Apps.Read(ctx, s.store.db, appID)
	if err != nil {
		return nil, err
	}
	var refs envref.Vars
	out := []string{}
	for _, v := range envstore.Open(s.box, vars) {
		val := v.Value
		if val == "" {
			continue
		}
		if len(envref.Find(val)) > 0 {
			if refs == nil {
				if refs, err = s.vars(ctx, a.ProjectID); err != nil {
					return nil, err
				}
			}
			if val, err = envref.Expand(val, refs); err != nil {
				return nil, fmt.Errorf("variable %s: %w", v.Key, err)
			}
		}
		out = append(out, v.Key+"="+val)
	}
	return out, nil
}
