package host

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GitClone clones url into dir (empty or missing) with the extra git args. With a GitHub token
// and a github.com repo it clones over https with the token first, which is how private repos
// clone without a key on the node. If that fails (the token expired, was revoked or doesn't
// reach this repo: GitHub then refuses even public repos), it clones as if there were no token,
// so public repos and repos the node's key reaches keep working. It returns the URL that
// worked, never with the token in it.
func GitClone(ctx context.Context, args []string, url, dir, token string) (string, error) {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=" + GitSSH}
	https := HTTPSURL(url)
	if token == "" || !isGitHub(https) {
		return cloneWithout(ctx, env, args, url, dir)
	}
	err := Run(ctx, append(env, tokenEnv(gitHub, token)...), "git", append(append(append([]string{}, args...), "--"), https, dir)...)
	if err == nil {
		return https, nil
	}
	if b := missingBranch(err); b != nil { // the token got in: no other way will find the branch
		return url, b
	}
	if err := emptyDir(dir); err != nil {
		return url, err
	}
	used, err2 := cloneWithout(ctx, env, args, url, dir)
	if err2 != nil {
		return url, fmt.Errorf("with the hub's GitHub token: %v (expired, revoked, or it doesn't reach this repo: a fine-grained token needs Contents: read on it); without it: %v", err, err2)
	}
	return used, nil
}

// cloneWithout clones with the node's own git: an SSH URL first with the node user's SSH keys,
// then its https form, so public repos still clone on a node without keys (GitHub wants a key
// even for public repos over SSH).
// ponytail: accept-new trusts a forge's host key the first time the node sees it.
func cloneWithout(ctx context.Context, env, args []string, url, dir string) (string, error) {
	clone := func(u string) error {
		return Run(ctx, env, "git", append(append(append([]string{}, args...), "--"), u, dir)...)
	}
	err := clone(url)
	https := HTTPSURL(url)
	if b := missingBranch(err); b != nil {
		return url, b
	}
	if err != nil && https == url && strings.Contains(url, "://") { // an https URL: no SSH to fall back from
		return url, fmt.Errorf("%v (%s)", err, privateHint)
	}
	if err == nil || https == url {
		return url, err
	}
	if err := emptyDir(dir); err != nil {
		return url, err
	}
	if err2 := clone(https); err2 != nil {
		return url, fmt.Errorf("over SSH: %v; over https: %v (%s)", err, err2, privateHint)
	}
	return https, nil
}

const privateHint = "a private repo: add a GitHub token in Settings, or give the node an SSH key with access to it"

// tokenEnv authenticates git's https requests to github.com with token, through git's config
// in the environment (GIT_CONFIG_*, git 2.31+): not in the URL, the clone's .git/config, the
// process list or an error.
// base scopes it to one site: git sends it to no other.
func tokenEnv(base, token string) []string {
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http." + base + ".extraheader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + auth}
}

var branchRe = regexp.MustCompile(`Remote branch (\S+) not found`)

// missingBranch is a clear error when git got into the repo but it has no such branch, nil for
// any other error: then there's nothing to retry another way.
func missingBranch(err error) error {
	if err == nil {
		return nil
	}
	if m := branchRe.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("the repo has no branch %q: check the branch name, or leave it empty for the default branch", m[1])
	}
	return nil
}

// gitHub is where the token goes; tests point it at a fake GitHub.
var gitHub = "https://github.com/"

func isGitHub(u string) bool {
	p, err := url.Parse(u)
	g, _ := url.Parse(gitHub)
	return err == nil && p.Scheme == g.Scheme && p.Host == g.Host
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
