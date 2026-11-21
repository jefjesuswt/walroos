package workspaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

var ErrDetectClone = errors.New("can't clone the repo")

// Detect shallow-clones the repo into a temp dir, scans it and removes the clone.
func Detect(ctx context.Context, repoURL, branch string) (agent.Detection, error) {
	if repoURL == "" {
		return agent.Detection{}, fmt.Errorf("%w: repo_url is required", ErrDetectClone)
	}
	dir, err := os.MkdirTemp("", "walroos-detect-")
	if err != nil {
		return agent.Detection{}, err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second) // the hub gives up on agents at 30s
	defer cancel()
	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	if _, err := host.GitClone(ctx, args, repoURL, dir); err != nil {
		return agent.Detection{}, fmt.Errorf("%w: %v", ErrDetectClone, err)
	}
	d := agent.DetectDir(dir)
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		d.Branch = strings.TrimSpace(string(out))
	}
	return d, nil
}
