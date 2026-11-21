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
	sc := sshScan{at: -1, suffix: suffix}
	for _, line := range lines {
		if err := sc.line(line); err != nil {
			return nil, false, nil, err
		}
	}
	if sc.inside {
		return nil, false, nil, errors.New("~/.ssh/config has \"# BEGIN walroos\" without its \"# END walroos\"; fix it by hand, then run this again")
	}
	out = place(append(sc.kept, sc.held...), sc.at, block)
	return out, !bytes.Equal(out, config), sc.dropped, nil
}

// sshScan walks the config line by line, keeping what isn't walroos': the managed section
// goes (at remembers where), and so do stray Host blocks for our workspaces pasted by hand.
type sshScan struct {
	kept, held, dropped []string // held: comments and blank lines after a dropped block's last option
	at                  int      // where the managed section was, in kept
	inside, dropping    bool
	suffix              string
}

func (sc *sshScan) line(line string) error {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, beginMarker):
		if sc.inside || sc.at >= 0 {
			return errors.New("~/.ssh/config has two walroos sections; fix it by hand, then run this again")
		}
		sc.kept, sc.held, sc.dropping = append(sc.kept, sc.held...), nil, false
		sc.inside, sc.at = true, len(sc.kept)
		return nil
	case strings.HasPrefix(t, endMarker):
		if !sc.inside {
			return errors.New("~/.ssh/config has an \"# END walroos\" line without its \"# BEGIN walroos\"; fix it by hand, then run this again")
		}
		sc.inside = false
		return nil
	case sc.inside, strings.HasPrefix(t, printedHeader):
		return nil // the header `ssh-config >>` pasted along with the blocks
	}
	if kw, args := directive(t); (kw == "host" || kw == "match") && sc.startBlock(kw, args) {
		return nil
	}
	switch {
	case !sc.dropping:
		sc.kept = append(sc.kept, line)
	case t == "" || strings.HasPrefix(t, "#"):
		sc.held = append(sc.held, line) // likely about the next block: keep unless more options follow
	default:
		sc.held = nil
	}
	return nil
}

// startBlock begins a Host or Match block; it reports whether the line is dropped (a Host
// block that only names our workspaces).
func (sc *sshScan) startBlock(kw string, args []string) bool {
	sc.kept, sc.held = append(sc.kept, sc.held...), nil
	sc.dropping = kw == "host" && allOurs(args, sc.suffix)
	if !sc.dropping {
		return false
	}
	sc.dropped = append(sc.dropped, strings.Join(args, " "))
	for len(sc.kept) > 0 && strings.TrimSpace(sc.kept[len(sc.kept)-1]) == "" {
		sc.kept = sc.kept[:len(sc.kept)-1] // its separator: the blank lines after it take its place
	}
	return true
}

// place writes kept with block where the old section was, or at the end after a blank line.
func place(kept []string, at int, block string) []byte {
	if at < 0 && block != "" {
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
	return b.Bytes()
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
