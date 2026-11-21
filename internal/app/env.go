package app

import (
	"context"

	"github.com/jefjesuswt/walroos/internal/workspace"
)

// An app's variables are sealed in the hub and handed to its container on each deploy.

func (s *Service) Env(ctx context.Context, name string) ([]workspace.EnvVar, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := s.store.Env(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	for i := range vars {
		v, err := s.box.Open(vars[i].Value)
		if err != nil {
			v = "" // wrong key or corrupt: don't break the whole list
		}
		vars[i].Value = v
	}
	return vars, nil
}

// SetEnv replaces the variables; they apply on the next deploy.
func (s *Service) SetEnv(ctx context.Context, name string, vars []workspace.EnvVar) error {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return err
	}
	sealed, err := workspace.SealEnv(s.box, vars)
	if err != nil {
		return err
	}
	return s.store.ReplaceEnv(ctx, a.ID, sealed)
}

func (s *Service) envForAgent(ctx context.Context, appID string) ([]string, error) {
	vars, err := s.store.Env(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range vars {
		if val, err := s.box.Open(v.Value); err == nil && val != "" {
			out = append(out, v.Key+"="+val)
		}
	}
	return out, nil
}
