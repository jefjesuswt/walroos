package database

import (
	"context"
	"strconv"

	"github.com/jefjesuswt/walroos/shared/envref"
)

// RefVars are what an app's ${{<database>.<KEY>}} references can name, for each database of the
// project: HOST, PORT, USER, PASSWORD, DATABASE and URL (the connection string with the password).
func (s *Service) RefVars(ctx context.Context, projectID string) (envref.Vars, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	vars := envref.Vars{}
	for _, d := range list {
		if d.ProjectID != projectID {
			continue
		}
		d = s.withConn(d, true)
		pw, _ := s.box.Open(d.password)
		v := map[string]string{"HOST": d.Host, "PORT": strconv.Itoa(d.Port), "PASSWORD": pw, "URL": d.URL}
		if d.User != "" {
			v["USER"], v["DATABASE"] = d.User, d.DB
		}
		vars[d.Name] = v
	}
	return vars, nil
}
