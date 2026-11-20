package sshconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

// Zed's "Open Remote" lists ssh_connections from its settings.json, and a connection's projects
// open with one click. Without a project, connecting over SSH lands in the remote $HOME instead of
// the repo. These helpers keep one entry per workspace host with its repo folder as a project.

// zedEntries is what `--zed` prints: the ssh_connections entries for the workspaces.
func zedEntries(list []workspace, suffix string) []map[string]any {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	out := []map[string]any{}
	for _, ws := range list {
		e := map[string]any{"host": ws.Name + suffix, "projects": []any{}}
		if ws.Folder != "" {
			e["projects"] = []any{map[string]any{"paths": []any{ws.Folder}}}
		}
		out = append(out, e)
	}
	return out
}

// MergeZed returns settings (Zed's settings.json, JSON with comments) with ssh_connections
// updated for the workspaces. Comments and everything outside ssh_connections stay as they were.
// In ssh_connections:
//   - entries for other hosts are kept as they are;
//   - one entry per workspace host (duplicates merged), keeping its other fields (args,
//     nickname...) and projects, plus the workspace's folder if missing;
//   - entries for hosts ending in suffix that are no longer workspaces are dropped.
//
// Workspaces without a known folder (node unreachable) keep what they had, and are reported.
// It returns changed=false when nothing would change, so callers can skip the write.
func MergeZed(settings []byte, list []workspace, suffix string) (out []byte, changed bool, warnings []string, err error) {
	if len(bytes.TrimSpace(settings)) == 0 {
		settings = []byte("{}\n")
	}
	v, err := hujson.Parse(settings)
	if err != nil {
		return nil, false, nil, fmt.Errorf("Zed's settings.json isn't valid JSON (with comments): %w", err)
	}
	std := v.Clone()
	std.Standardize()
	var doc map[string]any
	if err := json.Unmarshal(std.Pack(), &doc); err != nil || doc == nil {
		return nil, false, nil, errors.New("Zed's settings.json isn't a JSON object")
	}
	var current []any
	if raw, ok := doc["ssh_connections"]; ok && raw != nil {
		if current, ok = raw.([]any); !ok {
			return nil, false, nil, errors.New(`Zed's "ssh_connections" isn't a list`)
		}
	}

	// Before anything below touches them: the entries kept are the same maps, edited in place.
	before, _ := json.Marshal(current)

	wanted := map[string]workspace{}
	for _, ws := range list {
		wanted[ws.Name+suffix] = ws
	}
	var others []any
	ours := map[string]map[string]any{}
	for _, raw := range current {
		e, ok := raw.(map[string]any)
		host, _ := e["host"].(string)
		switch {
		case !ok || host == "":
			others = append(others, raw) // not ours to judge
		case wanted[host].Name != "":
			if prev := ours[host]; prev != nil { // a duplicate: keep the first, add its projects
				prev["projects"] = append(projectsOf(prev), projectsOf(e)...)
			} else {
				ours[host] = e
			}
		case strings.HasSuffix(host, suffix):
			// a workspace that no longer exists
		default:
			others = append(others, raw)
		}
	}

	names := make([]string, 0, len(wanted))
	for h := range wanted {
		names = append(names, h)
	}
	sort.Strings(names)
	merged := others
	for _, h := range names {
		ws := wanted[h]
		e := ours[h]
		if e == nil {
			e = map[string]any{"host": h, "projects": []any{}}
		}
		projects := dedupeProjects(projectsOf(e))
		if ws.Folder == "" {
			warnings = append(warnings, fmt.Sprintf("%s: its node didn't answer, so its folder isn't known; kept what Zed had", ws.Name))
		} else if !hasPath(projects, ws.Folder) {
			projects = append(projects, map[string]any{"paths": []any{ws.Folder}})
		}
		e["projects"] = projects
		merged = append(merged, e)
	}
	if merged == nil {
		merged = []any{}
	}

	after, _ := json.Marshal(merged)
	if bytes.Equal(before, after) || (len(current) == 0 && len(merged) == 0) {
		return settings, false, warnings, nil
	}
	val, err := json.MarshalIndent(merged, "  ", "  ") // Zed's own two-space style, as a top-level member
	if err != nil {
		return nil, false, nil, err
	}
	// Splice the text in rather than re-serializing: every other byte of the file stays as is
	// (hujson's Patch would also work, but writes the new value on one line).
	obj, _ := v.Value.(*hujson.Object)
	if obj == nil {
		return nil, false, nil, errors.New("Zed's settings.json isn't a JSON object")
	}
	for _, m := range obj.Members {
		if name, _ := m.Name.Value.(hujson.Literal); name.String() == "ssh_connections" {
			out := append(append(append([]byte{}, settings[:m.Value.StartOffset]...), val...), settings[m.Value.EndOffset:]...)
			return out, true, warnings, nil
		}
	}
	insert := "\n  \"ssh_connections\": " + string(val)
	if len(obj.Members) > 0 {
		insert += ","
	} else {
		insert += "\n"
	}
	brace := v.StartOffset + 1 // right after the opening brace
	out = append(append(append([]byte{}, settings[:brace]...), insert...), settings[brace:]...)
	return out, true, warnings, nil
}

func projectsOf(e map[string]any) []any {
	p, _ := e["projects"].([]any)
	return p
}

func hasPath(projects []any, path string) bool {
	for _, p := range projects {
		if m, ok := p.(map[string]any); ok {
			if paths, ok := m["paths"].([]any); ok {
				for _, x := range paths {
					if x == path {
						return true
					}
				}
			}
		}
	}
	return false
}

// dedupeProjects drops projects whose paths repeat an earlier one (duplicated entries merged).
func dedupeProjects(projects []any) []any {
	seen := map[string]bool{}
	out := []any{}
	for _, p := range projects {
		k, _ := json.Marshal(p)
		if !seen[string(k)] {
			seen[string(k)] = true
			out = append(out, p)
		}
	}
	return out
}

// zedSettingsPath is Zed's settings.json on Linux (and macOS: Zed uses ~/.config there too).
func zedSettingsPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "zed", "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "zed", "settings.json")
}

// writeZed updates the settings file in place (symlinks followed, mode kept).
func writeZed(path string, list []workspace, suffix string) (changed bool, warnings []string, err error) {
	target := resolve(path)
	settings, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, nil, err
	}
	out, changed, warnings, err := MergeZed(settings, list, suffix)
	if err != nil || !changed {
		return false, warnings, err
	}
	return true, warnings, replaceFile(target, out, 0o644, 0o755)
}
