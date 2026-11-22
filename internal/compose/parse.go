// Package compose turns a docker-compose.yml into a project's apps: each service becomes an app
// (named <project>-<service>, reachable by its service name on the project's network), with its
// image or build, environment, volumes, command and the order depends_on asks for. Only what
// walroos can keep is accepted; the rest is refused with the reason, or ignored with a warning.
package compose

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the part of a compose file walroos reads.
type File struct {
	Services map[string]Spec
	Volumes  map[string]yaml.Node // named volumes it declares
	Warnings []string
}

// Spec is one service, as written.
type Spec struct {
	Image       string
	Build       *Build
	Command     []string
	Environment []string // KEY=VALUE, in the file's order; env_file entries first
	EnvFiles    []string
	Ports       []int // container ports, the first is the app's
	Volumes     []Volume
	DependsOn   []string
}

type Build struct{ Context, Dockerfile string }

// Volume is a named volume mounted at Target (an anonymous one gets a name from its target).
type Volume struct {
	Name, Target string
	ReadOnly     bool
}

// Accepted service keys; the second list is read but ignored (with a warning): walroos does
// these its own way (restarts, health checks, its project network, the container's name).
var (
	serviceKeys = []string{"image", "build", "command", "environment", "env_file", "ports", "expose", "volumes", "depends_on"}
	ignoredKeys = []string{"restart", "healthcheck", "container_name", "networks", "labels", "hostname", "stop_grace_period",
		"deploy", "logging", "tty", "stdin_open", "init", "pull_policy"}
	topKeys = []string{"services", "volumes", "networks", "name", "version"}
)

// Parse reads text after interpolating ${VAR} from vars (the project's shared variables).
func Parse(text string, vars map[string]string) (*File, error) {
	text, err := interpolate(text, vars)
	if err != nil {
		return nil, err
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(text), &top); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	f := &File{Services: map[string]Spec{}}
	for _, k := range sortedKeys(top) {
		switch {
		case strings.HasPrefix(k, "x-"):
		case !slices.Contains(topKeys, k):
			return nil, fmt.Errorf("%s: not supported by walroos", k)
		case k == "networks":
			f.warn("networks: ignored; the project's services share one network and reach each other by service name")
		}
	}
	if n, ok := top["volumes"]; ok {
		if err := n.Decode(&f.Volumes); err != nil {
			return nil, fmt.Errorf("volumes: %w", err)
		}
		for name, v := range f.Volumes {
			var opts struct{ External bool }
			v.Decode(&opts)
			if opts.External {
				return nil, fmt.Errorf("volumes.%s: an external volume isn't supported: walroos creates each app's volumes", name)
			}
		}
	}
	var services map[string]yaml.Node
	if n, ok := top["services"]; !ok || n.Decode(&services) != nil || len(services) == 0 {
		return nil, fmt.Errorf("services: none; a compose file needs at least one")
	}
	for _, name := range sortedKeys(services) {
		n := services[name]
		s, err := f.service(name, &n)
		if err != nil {
			return nil, fmt.Errorf("services.%s: %w", name, err)
		}
		f.Services[name] = s
	}
	return f, nil
}

func (f *File) warn(format string, args ...any) {
	f.Warnings = append(f.Warnings, fmt.Sprintf(format, args...))
}

func (f *File) service(name string, n *yaml.Node) (Spec, error) {
	var keys map[string]yaml.Node
	if err := n.Decode(&keys); err != nil {
		return Spec{}, err
	}
	var s Spec
	for _, k := range sortedKeys(keys) {
		v := keys[k]
		var err error
		switch {
		case slices.Contains(ignoredKeys, k):
			f.warn("services.%s.%s: ignored", name, k)
		case !slices.Contains(serviceKeys, k):
			return s, fmt.Errorf("%s: not supported by walroos", k)
		case k == "image":
			err = v.Decode(&s.Image)
		case k == "build":
			s.Build, err = decodeBuild(&v)
		case k == "command":
			s.Command, err = decodeCommand(&v)
		case k == "environment":
			s.Environment, err = decodeEnv(&v)
		case k == "env_file":
			s.EnvFiles, err = stringOrList(&v)
		case k == "ports", k == "expose":
			var ports []int
			ports, err = decodePorts(&v)
			s.Ports = append(s.Ports, ports...)
		case k == "volumes":
			s.Volumes, err = decodeVolumes(&v)
		case k == "depends_on":
			s.DependsOn, err = decodeDependsOn(&v)
		}
		if err != nil {
			return s, fmt.Errorf("%s: %w", k, err)
		}
	}
	if s.Image == "" && s.Build == nil {
		return s, fmt.Errorf("needs an image or a build")
	}
	return s, nil
}

var varRe = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?])([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// interpolate replaces ${VAR}, ${VAR:-default}, ${VAR-default}, ${VAR:?message} and $VAR like
// docker compose, from vars; $$ is a literal $. A variable with no value and no default is an
// error (compose would quietly use ""), listing them all.
// ponytail: done on the text, so a ${VAR} in a YAML comment counts too.
func interpolate(text string, vars map[string]string) (string, error) {
	var missing, failed []string
	out := varRe.ReplaceAllStringFunc(text, func(m string) string {
		if m == "$$" {
			return "$"
		}
		g := varRe.FindStringSubmatch(m)
		name, op, arg := g[1], g[2], g[3]
		if name == "" {
			name = g[4]
		}
		v, set := vars[name]
		empty := !set || op != "" && op[0] == ':' && v == ""
		switch {
		case !empty:
			return v
		case strings.HasSuffix(op, "-"):
			return arg
		case strings.HasSuffix(op, "?"):
			failed = append(failed, name+": "+arg)
		default:
			missing = append(missing, name)
		}
		return ""
	})
	if len(failed) > 0 {
		return "", fmt.Errorf("required variables: %s", strings.Join(failed, "; "))
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return "", fmt.Errorf("no value for %s: set them in the project's Variables (or give a default, ${VAR:-value})", strings.Join(slices.Compact(missing), ", "))
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
