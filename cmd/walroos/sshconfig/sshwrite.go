package sshconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// `walroos ssh-config --write` keeps the workspaces' Host blocks in ~/.ssh/config between two
// marker lines and replaces that section on every run, so running it again (a new workspace, a
// node that moved) never duplicates blocks the way `>> ~/.ssh/config` did.

const (
	beginMarker = "# BEGIN walroos"
	endMarker   = "# END walroos"
	// printedHeader starts the first line of Format's output.
	printedHeader = "# walroos workspaces"
)

// managedBlock is the section between the markers, "" when there are no workspaces.
func managedBlock(hosts string) string {
	if hosts == "" {
		return ""
	}
	return beginMarker + " (written by `walroos ssh-config --write`; changes in here are replaced)\n" +
		hosts + endMarker + "\n"
}

// MergeSSH returns config (an OpenSSH client config) with the managed section set to block:
// replaced where it was, or appended at the end the first time. Outside it, Host blocks for
// hosts ending in suffix (pasted from an earlier `>>`) are dropped and returned in dropped, since
// the managed section now has the current ones; wildcard patterns (Host *.walroos) are the user's
// and stay. Everything else stays byte for byte. A file with markers out of order is refused.
func MergeSSH(config []byte, block, suffix string) (out []byte, changed bool, dropped []string, err error) {
	lines := strings.SplitAfter(string(config), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var kept []string
	at := -1 // where the managed section was, in kept
	inside := false
	dropping := false
	var held []string // comments and blank lines after a dropped block's last option
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, beginMarker):
			if inside || at >= 0 {
				return nil, false, nil, errors.New("~/.ssh/config has two walroos sections; fix it by hand, then run this again")
			}
			kept, held, dropping = append(kept, held...), nil, false
			inside, at = true, len(kept)
			continue
		case strings.HasPrefix(t, endMarker):
			if !inside {
				return nil, false, nil, errors.New("~/.ssh/config has an \"# END walroos\" line without its \"# BEGIN walroos\"; fix it by hand, then run this again")
			}
			inside = false
			continue
		case inside, strings.HasPrefix(t, printedHeader):
			continue // the header `ssh-config >>` pasted along with the blocks
		}

		if kw, args := directive(t); kw == "host" || kw == "match" {
			kept, held = append(kept, held...), nil
			dropping = kw == "host" && allOurs(args, suffix)
			if dropping {
				dropped = append(dropped, strings.Join(args, " "))
				for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
					kept = kept[:len(kept)-1] // its separator: the blank lines after it take its place
				}
				continue
			}
		}
		switch {
		case !dropping:
			kept = append(kept, line)
		case t == "" || strings.HasPrefix(t, "#"):
			held = append(held, line) // likely about the next block: keep unless more options follow
		default:
			held = nil
		}
	}
	if inside {
		return nil, false, nil, errors.New("~/.ssh/config has \"# BEGIN walroos\" without its \"# END walroos\"; fix it by hand, then run this again")
	}
	kept = append(kept, held...)

	if at < 0 && block != "" { // first time: at the end, after a blank line
		if n := len(kept); n > 0 {
			if !strings.HasSuffix(kept[n-1], "\n") {
				kept[n-1] += "\n"
			}
			if strings.TrimSpace(kept[n-1]) != "" {
				kept = append(kept, "\n")
			}
		}
		at = len(kept)
	}
	var b bytes.Buffer
	for i, line := range kept {
		if i == at {
			b.WriteString(block)
		}
		b.WriteString(line)
	}
	if at == len(kept) {
		b.WriteString(block)
	}
	return b.Bytes(), !bytes.Equal(b.Bytes(), config), dropped, nil
}

// directive splits an ssh_config line into its lowercased keyword and arguments
// ("Host a b", "Host=a"); comments and blank lines give "".
func directive(t string) (string, []string) {
	if t == "" || strings.HasPrefix(t, "#") {
		return "", nil
	}
	kw, rest, _ := strings.Cut(t, " ")
	if k, r, ok := strings.Cut(kw, "="); ok {
		kw, rest = k, r+" "+rest
	}
	rest = strings.TrimPrefix(strings.TrimSpace(rest), "=")
	return strings.ToLower(strings.TrimSpace(kw)), strings.Fields(rest)
}

// allOurs: every pattern is a plain host name ending in suffix.
func allOurs(patterns []string, suffix string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, p := range patterns {
		if !strings.HasSuffix(p, suffix) || len(p) == len(suffix) || strings.ContainsAny(p, "*?!") {
			return false
		}
	}
	return true
}

func sshConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

// writeSSH updates the SSH config in place (symlinks followed, mode kept, 0600 if new).
func writeSSH(path, block, suffix string) (changed bool, dropped []string, err error) {
	target := resolve(path)
	config, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, nil, err
	}
	out, changed, dropped, err := MergeSSH(config, block, suffix)
	if err != nil {
		return false, nil, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, dropped, nil
	}
	return true, dropped, replaceFile(target, out, 0o600, 0o700)
}

// resolve follows a symlink (dotfiles), so the write lands on the real file and the link stays.
func resolve(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// replaceFile writes data to a temp file next to target and renames it over target, keeping
// target's mode (newMode if it doesn't exist yet). A failed write leaves target as it was.
func replaceFile(target string, data []byte, newMode, dirMode os.FileMode) error {
	mode := newMode
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
