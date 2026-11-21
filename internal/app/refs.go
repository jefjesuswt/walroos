package app

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/envref"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// Resolver gives the variables references can name in a project: every service's and the
// project's shared ones. The project package has it; the hub hands it to SetResolver.
type Resolver func(ctx context.Context, projectID string) (envref.Vars, error)

// SetResolver turns references on: without it a value with ${{...}} is refused.
func (s *Service) SetResolver(r Resolver) { s.resolve = r }

// RefVars are what ${{<app>.<KEY>}} can name, for each app of the project: HOST and PORT inside
// the project's network, and URL (http://<app>:<port>).
func (s *Service) RefVars(ctx context.Context, projectID string) (envref.Vars, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	vars := envref.Vars{}
	for _, a := range list {
		if a.ProjectID == projectID && a.Port > 0 {
			port := strconv.Itoa(a.Port)
			vars[a.Name] = map[string]string{"HOST": a.Name, "PORT": port, "URL": "http://" + a.Name + ":" + port}
		}
	}
	return vars, nil
}

// vars are the references the app's project offers.
func (s *Service) vars(ctx context.Context, projectID string) (envref.Vars, error) {
	if s.resolve == nil {
		return envref.Vars{}, nil
	}
	return s.resolve(ctx, projectID)
}

// checkRefs refuses variables whose references name nothing in the project.
func (s *Service) checkRefs(ctx context.Context, projectID string, vars []workspace.EnvVar) error {
	var refs envref.Vars
	for _, v := range vars {
		if len(envref.Find(v.Value)) == 0 {
			continue
		}
		if refs == nil {
			var err error
			if refs, err = s.vars(ctx, projectID); err != nil {
				return err
			}
		}
		if err := envref.Check(v.Value, refs); err != nil {
			return whttp.Invalid(fmt.Sprintf("%s: %v", v.Key, err))
		}
	}
	return nil
}
