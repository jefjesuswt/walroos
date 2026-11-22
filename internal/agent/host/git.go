package host

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// gitClone clones url into dir (empty or missing) with the extra git args. With a GitHub token
// and a github.com repo it clones over https with the token, which is how private repos clone
// without a key on the node. Otherwise an SSH URL is tried first with the node user's own SSH
// keys; if that fails it falls back to the https form, so public repos still clone on a node
// without keys (GitHub wants a key even for public repos over SSH). It returns the URL that
// worked, never with the token in it.
// ponytail: accept-new trusts a forge's host key the first time the node sees it.
func GitClone(ctx context.Context, args []string, url, dir, token string) (string, error) {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=" + GitSSH}
	if https := HTTPSURL(url); token != "" && isGitHub(https) {
		err := Run(ctx, append(env, tokenEnv(token)...), "git", append(append(append([]string{}, args...), "--"), https, dir)...)
		if err != nil {
			return url, fmt.Errorf("%v (with the hub's GitHub token: does it reach this repo? A fine-grained token needs Contents: read on it)", err)
		}
		return https, nil
	}
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
		return url, fmt.Errorf("over SSH: %v; over https: %v (a private repo: add a GitHub token in Settings, or give the node an SSH key with access to it)", err, err2)
	}
	return https, nil
}

// tokenEnv authenticates git's https requests to github.com with token, through git's config
// in the environment (GIT_CONFIG_*, git 2.31+): not in the URL, the clone's .git/config, the
// process list or an error.
func tokenEnv(token string) []string {
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + auth}
}

func isGitHub(u string) bool {
	p, err := url.Parse(u)
	return err == nil && p.Scheme == "https" && p.Host == "github.com"
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
