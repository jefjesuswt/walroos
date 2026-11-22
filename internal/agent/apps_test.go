package agent

import "testing"

func TestAppSpecHealthCommandContext(t *testing.T) {
	base := AppSpec{Name: "web", ID: "d1", RepoURL: "https://example.com/web.git", Port: 8080}
	cases := []struct {
		name string
		edit func(*AppSpec)
		ok   bool
	}{
		{"http by default", func(s *AppSpec) {}, true},
		{"tcp", func(s *AppSpec) { s.Health = HealthTCP }, true},
		{"tcp needs a port", func(s *AppSpec) { s.Health, s.Port = HealthTCP, 0 }, false},
		{"a worker has no port", func(s *AppSpec) { s.Health, s.Port = HealthRunning, 0 }, true},
		{"an unknown check", func(s *AppSpec) { s.Health = "grpc" }, false},
		{"a command", func(s *AppSpec) { s.Command = []string{"sh", "-c", "echo $HOME 100%"} }, true},
		{"a newline in the command", func(s *AppSpec) { s.Command = []string{"sh", "-c", "a\nb"} }, false},
		{"a context folder", func(s *AppSpec) { s.Context = "services/api" }, true},
		{"a context outside the repo", func(s *AppSpec) { s.Context = "../etc" }, false},
		{"an absolute context", func(s *AppSpec) { s.Context = "/srv" }, false},
		{"an unclean context", func(s *AppSpec) { s.Context = "a/../b" }, false},
	}
	for _, c := range cases {
		s := base
		c.edit(&s)
		if s.Valid() != c.ok {
			t.Errorf("%s: Valid() = %v", c.name, !c.ok)
		}
	}
}
