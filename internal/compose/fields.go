package compose

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Compose fields come short (a string) or long (a list or a map); these read both.

func stringOrList(n *yaml.Node) ([]string, error) {
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}, nil
	}
	var l []string
	err := n.Decode(&l)
	return l, err
}

func decodeBuild(n *yaml.Node) (*Build, error) {
	if n.Kind == yaml.ScalarNode {
		return &Build{Context: n.Value}, nil
	}
	var b struct {
		Context, Dockerfile string
		Args                yaml.Node
		DockerfileInline    string `yaml:"dockerfile_inline"`
		Target              string
	}
	if err := n.Decode(&b); err != nil {
		return nil, err
	}
	switch {
	case !b.Args.IsZero():
		return nil, fmt.Errorf("args aren't supported: give the Dockerfile's ARGs defaults")
	case b.DockerfileInline != "":
		return nil, fmt.Errorf("dockerfile_inline isn't supported: commit the Dockerfile")
	case b.Target != "":
		return nil, fmt.Errorf("target isn't supported: walroos builds the Dockerfile's last stage")
	}
	return &Build{Context: b.Context, Dockerfile: b.Dockerfile}, nil
}

func decodeCommand(n *yaml.Node) ([]string, error) {
	if n.Kind == yaml.ScalarNode {
		return splitCommand(n.Value), nil
	}
	var l []string
	err := n.Decode(&l)
	return l, err
}

// splitCommand splits a command line like a shell would, without expanding anything: what
// compose does with command as a string.
func splitCommand(line string) []string {
	var args []string
	var cur strings.Builder
	inArg, quote := false, rune(0)
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote == '"' && c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
		case quote != 0:
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote, inArg = c, true
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			inArg = true
		case unicode.IsSpace(c):
			if inArg {
				args, inArg = append(args, cur.String()), false
				cur.Reset()
			}
		default:
			cur.WriteRune(c)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args
}

// decodeEnv reads environment as a map (KEY: value) or a list (KEY=value). A KEY with no value
// takes it from the shell in compose; walroos has no shell to take it from, so it's an error.
func decodeEnv(n *yaml.Node) ([]string, error) {
	var out []string
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i].Value, n.Content[i+1]
			if v.Tag == "!!null" {
				return nil, fmt.Errorf("%s has no value: give it one (walroos has no shell to take it from)", k)
			}
			out = append(out, k+"="+v.Value)
		}
		return out, nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return nil, err
	}
	for _, kv := range l {
		if !strings.Contains(kv, "=") {
			return nil, fmt.Errorf("%s has no value: give it one (walroos has no shell to take it from)", kv)
		}
	}
	return l, nil
}

var portRe = regexp.MustCompile(`^(?:(?:[0-9.]+|\[[0-9a-f:]+\]):)?(?:[0-9]+:)?([0-9]+)(?:/(tcp|udp))?$`)

// decodePorts returns the container ports of ports or expose: "8080:80" and "80" are port 80
// (walroos publishes no host ports: domains and other services reach the container's).
func decodePorts(n *yaml.Node) ([]int, error) {
	var items []yaml.Node
	if err := n.Decode(&items); err != nil {
		return nil, err
	}
	var ports []int
	for _, it := range items {
		var p int
		if it.Kind == yaml.MappingNode {
			var long struct {
				Target   int
				Protocol string
			}
			if err := it.Decode(&long); err != nil || long.Target == 0 || long.Protocol == "udp" {
				return nil, fmt.Errorf("a port needs a target, over TCP")
			}
			p = long.Target
		} else {
			m := portRe.FindStringSubmatch(it.Value)
			if m == nil || m[2] == "udp" {
				return nil, fmt.Errorf("%q: walroos takes a single TCP port (8080:80, 80), not ranges or UDP", it.Value)
			}
			p, _ = strconv.Atoi(m[1])
		}
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("port %d is out of range", p)
		}
		ports = append(ports, p)
	}
	return ports, nil
}

var volNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// decodeVolumes keeps named and anonymous volumes. Binds (./config:/etc/app, /srv:/data) would
// need the repo's files or the node's on the node: refused, with what to do instead.
func decodeVolumes(n *yaml.Node) ([]Volume, error) {
	var items []yaml.Node
	if err := n.Decode(&items); err != nil {
		return nil, err
	}
	var vols []Volume
	for _, it := range items {
		var v Volume
		var typ string
		if it.Kind == yaml.MappingNode {
			var long struct {
				Type, Source, Target string
				ReadOnly             bool `yaml:"read_only"`
			}
			if err := it.Decode(&long); err != nil {
				return nil, err
			}
			typ, v = long.Type, Volume{Name: long.Source, Target: long.Target, ReadOnly: long.ReadOnly}
		} else {
			parts := strings.Split(it.Value, ":")
			if len(parts) == 1 {
				parts = []string{"", parts[0]}
			}
			v = Volume{Name: parts[0], Target: parts[1], ReadOnly: len(parts) > 2 && strings.Contains(parts[2], "ro")}
			if strings.HasPrefix(v.Name, ".") || strings.HasPrefix(v.Name, "/") || strings.HasPrefix(v.Name, "~") {
				typ = "bind"
			}
		}
		switch {
		case typ == "bind":
			return nil, fmt.Errorf("%s: a bind mount isn't supported: COPY the files into the image, or use a named volume", it.Value)
		case typ != "" && typ != "volume":
			return nil, fmt.Errorf("a %s mount isn't supported", typ)
		case !strings.HasPrefix(v.Target, "/"):
			return nil, fmt.Errorf("%q: the target must be an absolute path", v.Target)
		}
		if v.Name == "" { // anonymous: one per target, kept across deploys like compose's are not
			v.Name = "anon" + strings.NewReplacer("/", "-", "_", "-").Replace(strings.ToLower(v.Target))
		}
		if !volNameRe.MatchString(v.Name) {
			return nil, fmt.Errorf("volume name %q: lowercase letters, digits and '_.-'", v.Name)
		}
		vols = append(vols, v)
	}
	return vols, nil
}

func decodeDependsOn(n *yaml.Node) ([]string, error) {
	if n.Kind == yaml.MappingNode {
		var m map[string]yaml.Node // service: {condition: ...}; walroos deploys in order, each healthy first
		err := n.Decode(&m)
		return sortedKeys(m), err
	}
	return stringOrList(n)
}

// envFile reads a .env file: KEY=VALUE lines, # comments, optional quotes around the value.
func envFile(text string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sc.Text()), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out = append(out, strings.TrimSpace(k)+"="+v)
	}
	return out
}
