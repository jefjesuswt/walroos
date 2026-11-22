package compose

import (
	"fmt"
	"path"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/shared/names"
)

// Source is where a compose file comes from: a repo (RepoURL, Branch, Path of the file in it)
// or pasted (RepoURL "", Text).
type Source struct {
	RepoURL string `json:"repo_url,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Path    string `json:"path,omitempty"` // docker-compose.yml when empty
	Text    string `json:"text,omitempty"` // the pasted file
}

// File is the compose file's path in the repo.
func (s Source) File() string {
	if s.Path == "" {
		return "docker-compose.yml"
	}
	return path.Clean(s.Path)
}

// Planned is one service as the app it becomes.
type Planned struct {
	Service   string          `json:"service"` // its name in the compose file, the app's alias
	App       string          `json:"app"`     // <project>-<service>
	Input     app.SourceInput `json:"source"`
	Env       []string        `json:"-"` // KEY=VALUE; secrets, never sent back
	EnvKeys   []string        `json:"env"`
	Volumes   []agent.Volume  `json:"volumes"`
	DependsOn []string        `json:"depends_on,omitempty"`
}

// Translate turns f into the apps of project, in the order depends_on asks for. files has the
// env_files by their path in the repo.
func Translate(f *File, project string, src Source, files map[string]string) ([]Planned, error) {
	order, err := order(f.Services)
	if err != nil {
		return nil, err
	}
	dir := path.Dir(src.File())
	var plan []Planned
	for _, name := range order {
		s := f.Services[name]
		p := Planned{Service: name, App: project + "-" + name, DependsOn: s.DependsOn, EnvKeys: []string{}, Volumes: []agent.Volume{}} // [] not null: the UI reads .length
		if !names.DNSLabel(name) || !names.DNSLabel(p.App) {
			return nil, fmt.Errorf("services.%s: the app would be %s: service names must be lowercase letters, digits and '-', and %s-<service> at most 63", name, p.App, project)
		}
		if p.Input, err = source(s, src, dir); err != nil {
			return nil, fmt.Errorf("services.%s: %w", name, err)
		}
		if p.Env, err = env(s, dir, files); err != nil {
			return nil, fmt.Errorf("services.%s: %w", name, err)
		}
		for _, kv := range p.Env {
			k, _, _ := strings.Cut(kv, "=")
			p.EnvKeys = append(p.EnvKeys, k)
		}
		for _, v := range s.Volumes {
			p.Volumes = append(p.Volumes, agent.Volume{Name: v.Name, Target: v.Target, RO: v.ReadOnly})
		}
		plan = append(plan, p)
	}
	return plan, nil
}

// source is how the app gets its image (built from the repo, or pulled), runs and is checked: a
// service with a port by TCP (it may not speak HTTP), one without by staying up.
func source(s Spec, src Source, dir string) (app.SourceInput, error) {
	in := app.SourceInput{Command: s.Command, Health: agent.HealthRunning}
	if len(s.Ports) > 0 {
		in.Port, in.Health = s.Ports[0], agent.HealthTCP
	}
	if s.Build == nil {
		in.Image = normalizeImage(s.Image)
		return in, nil
	}
	if src.RepoURL == "" {
		return in, fmt.Errorf("build needs the compose file from a repo: a pasted one has nothing to build from")
	}
	ctx := path.Clean(path.Join(dir, s.Build.Context))
	if ctx == ".." || strings.HasPrefix(ctx, "../") || path.IsAbs(s.Build.Context) {
		return in, fmt.Errorf("build.context %s is outside the repo", s.Build.Context)
	}
	if ctx == "." {
		ctx = ""
	}
	in.RepoURL, in.Branch, in.Context, in.Dockerfile = src.RepoURL, src.Branch, ctx, s.Build.Dockerfile
	return in, nil
}

// env is the service's env_files, then its environment, which wins.
func env(s Spec, dir string, files map[string]string) ([]string, error) {
	var out []string
	for _, f := range s.EnvFiles {
		p := path.Clean(path.Join(dir, f))
		text, ok := files[p]
		if !ok {
			return nil, fmt.Errorf("env_file %s: not in the repo (or the compose file was pasted)", f)
		}
		out = append(out, envFile(text)...)
	}
	return append(out, s.Environment...), nil
}

// normalizeImage fully qualifies a Docker Hub name, as walroos pulls only those:
// postgres:18 → docker.io/library/postgres:18, me/app → docker.io/me/app.
func normalizeImage(img string) string {
	first, _, hasSlash := strings.Cut(img, "/")
	switch {
	case hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost"):
		return img
	case hasSlash:
		return "docker.io/" + img
	}
	return "docker.io/library/" + img
}

// order sorts the services so each comes after what it depends on (alphabetically otherwise).
func order(services map[string]Spec) ([]string, error) {
	var out []string
	done, visiting := map[string]bool{}, map[string]bool{}
	var visit func(name string, from string) error
	visit = func(name, from string) error {
		if _, ok := services[name]; !ok {
			return fmt.Errorf("services.%s: depends_on %s, which isn't a service", from, name)
		}
		if done[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("services.%s: depends_on goes in a circle", name)
		}
		visiting[name] = true
		for _, d := range services[name].DependsOn {
			if err := visit(d, name); err != nil {
				return err
			}
		}
		done[name] = true
		out = append(out, name)
		return nil
	}
	for _, name := range sortedKeys(services) {
		if err := visit(name, name); err != nil {
			return nil, err
		}
	}
	return out, nil
}
