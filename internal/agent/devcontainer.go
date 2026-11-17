package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// devcontainer is the subset of devcontainer.json walroos understands in v1.
// Spec: https://containers.dev/implementors/json_reference/
type devcontainer struct {
	Image string `json:"image"`
	Build struct {
		Dockerfile string `json:"dockerfile"`
		Context    string `json:"context"` // relative to the devcontainer.json folder; default "."
	} `json:"build"`
	ContainerEnv      map[string]string `json:"containerEnv"`
	RemoteUser        string            `json:"remoteUser"`
	ForwardPorts      []json.RawMessage `json:"forwardPorts"` // ports or "host:port"; reached via SSH forwarding, kept for the UI
	PostCreateCommand command           `json:"postCreateCommand"`
	Mounts            []mount           `json:"mounts"`

	dir string // folder holding devcontainer.json, to resolve build paths
}

// command is a devcontainer lifecycle command: a shell string or an argv array.
// ponytail: the object form (parallel named commands) is not supported.
type command []string

func (c *command) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s != "" {
			*c = command{"/bin/sh", "-c", s}
		}
		return nil
	}
	var argv []string
	if err := json.Unmarshal(b, &argv); err != nil {
		return errors.New("postCreateCommand must be a string or an array of strings")
	}
	*c = argv
	return nil
}

// mount is a devcontainer mount, kept as podman --mount syntax ("type=volume,source=x,target=/y").
type mount string

func (m *mount) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*m = mount(s)
		return nil
	}
	var o struct{ Type, Source, Target string }
	if err := json.Unmarshal(b, &o); err != nil || o.Target == "" {
		return errors.New("mounts entries must be strings or {type, source, target}")
	}
	if o.Type == "" {
		o.Type = "volume"
	}
	*m = mount(fmt.Sprintf("type=%s,source=%s,target=%s", o.Type, o.Source, o.Target))
	return nil
}

// safe reports whether the mount stays off the host filesystem: type=volume or type=tmpfs.
// A mount without type is rejected too (podman requires it anyway).
func (m mount) safe() bool {
	typed := false
	for _, kv := range strings.Split(string(m), ",") {
		if k, v, _ := strings.Cut(strings.TrimSpace(kv), "="); k == "type" { // every type= counts: "type=volume,type=bind"
			if v != "volume" && v != "tmpfs" {
				return false
			}
			typed = true
		}
	}
	return typed
}

// metadataRemoteUser reads the user from an image's devcontainer.metadata label, a JSON array
// of config fragments (one per feature, then the image's own). Each property is last-wins, and
// remoteUser falls back to containerUser, as in the spec. Images like mcr.microsoft.com/devcontainers/* set
// remoteUser there (e.g. "node") and run as root without it.
// ponytail: only the user is merged from image metadata, not containerEnv, mounts or lifecycle commands.
func metadataRemoteUser(label string) string {
	var entries []struct{ RemoteUser, ContainerUser string }
	if json.Unmarshal([]byte(label), &entries) != nil {
		return ""
	}
	var remote, container string
	for _, e := range entries {
		remote = cmpOr(e.RemoteUser, remote)
		container = cmpOr(e.ContainerUser, container)
	}
	return cmpOr(remote, container) // remoteUser defaults to containerUser
}

// devcontainerCandidates are the standard locations, in the order the spec looks for them.
var devcontainerCandidates = []string{".devcontainer/devcontainer.json", ".devcontainer.json"}

// readDevcontainer loads the config from repo. With path == "" it tries the standard locations
// and returns (nil, nil) when there is none, meaning "use the base image".
func readDevcontainer(repo, path string) (*devcontainer, error) {
	candidates := devcontainerCandidates
	if path != "" {
		candidates = []string{path}
	}
	for _, p := range candidates {
		full := filepath.Join(repo, filepath.Clean("/"+p)) // Clean("/"+p) keeps it inside repo
		raw, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) {
			if path == "" {
				continue
			}
			return nil, fmt.Errorf("devcontainer_path %s: not found in the repo", p)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		std, err := hujson.Standardize(raw) // devcontainer.json is JSONC: comments, trailing commas
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		dc := &devcontainer{dir: filepath.Dir(full)}
		if err := json.Unmarshal(std, dc); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if dc.Image == "" && dc.Build.Dockerfile == "" {
			return nil, fmt.Errorf("%s: needs image or build.dockerfile (docker compose is not supported yet)", p)
		}
		for _, m := range dc.Mounts {
			if !m.safe() {
				return nil, fmt.Errorf("%s: mount %q: only type=volume and type=tmpfs are allowed (a repo must not mount host paths)", p, m)
			}
		}
		return dc, nil
	}
	return nil, nil
}
