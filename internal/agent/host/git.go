package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gitClone clones url into dir (empty or missing) with the extra git args. An SSH URL is tried
// first with the node user's own SSH keys, which is how private repos clone; if that fails it
// falls back to the https form, so public repos still clone on a node without keys (GitHub
// wants a key even for public repos over SSH). It returns the URL that worked.
// ponytail: accept-new trusts a forge's host key the first time the node sees it.
func GitClone(ctx context.Context, args []string, url, dir string) (string, error) {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=" + GitSSH}
	clone := func(u string) error {
		return Run(ctx, env, "git", append(append(append([]string{}, args...), "--"), u, dir)...)
	}
	err := clone(url)
	https := HTTPSURL(url)
	if err == nil || https == url {
		return url, err
	}
	if err := emptyDir(dir); err != nil {
		return url, err
	}
	if err2 := clone(https); err2 != nil {
		return url, fmt.Errorf("over SSH: %v; over https: %v (a private repo needs an SSH key on the node with access to it)", err, err2)
	}
	return https, nil
}

// GitSSH never prompts (BatchMode: a key with a passphrase and no agent just fails) and accepts
// a forge's host key the first time, so a fresh node doesn't fail on known_hosts. Tests extend it.
var GitSSH = "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new"

// httpsURL turns an SSH remote (git@host:owner/repo, ssh://git@host/owner/repo) into its https
// form; anything else comes back as is.
func HTTPSURL(u string) string {
	if rest, ok := strings.CutPrefix(u, "ssh://"); ok {
		hostport, path, _ := strings.Cut(rest, "/")
		if _, h, ok := strings.Cut(hostport, "@"); ok {
			hostport = h
		}
		host, _, _ := strings.Cut(hostport, ":") // an SSH port means nothing to https
		return "https://" + host + "/" + path
	}
	if user, rest, ok := strings.Cut(u, "@"); ok && !strings.Contains(user, "/") && !strings.Contains(u, "://") {
		if host, path, ok := strings.Cut(rest, ":"); ok && !strings.Contains(host, "/") {
			return "https://" + host + "/" + strings.TrimPrefix(path, "/")
		}
	}
	return u
}

// emptyDir removes what a failed clone left in dir, keeping dir itself (a volume's mountpoint).
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
