// Package host runs the node's own programs for the agent: podman, systemctl, journalctl. Every
// call goes through the CLI (the podman Go bindings don't build everywhere walroos does), and a
// failure carries the command's stderr.
package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ensureVolume creates the workspace volume, or reuses it if it's ours (a retried create keeps
// the clone). A volume with that name but without our label belongs to someone else: refuse.
// ensureVolume creates volume labeled label=owner, or checks an existing one carries it.
func EnsureVolume(ctx context.Context, volume, label, owner string) error {
	got, err := Podman(ctx, "volume", "inspect", "--format", `{{index .Labels "`+label+`"}}`, volume)
	if err != nil { // doesn't exist
		_, err := Podman(ctx, "volume", "create", "--label", label+"="+owner, volume)
		return err
	}
	if got != owner {
		return fmt.Errorf("volume %s already exists and isn't %s's; remove or rename it", volume, owner)
	}
	return nil
}

// inside resolves p and fails if it escapes root (e.g. "dockerfile": "../../etc/passwd").
// Errors show the path relative to the repo, never the host's volume path.
func Inside(root, p string) (string, error) {
	// Resolve symlinks first: a repo can commit "ctx -> ../../../.." and point a build context or
	// a Dockerfile at the node's home. The resolved path is what callers use.
	name, _ := filepath.Rel(root, p)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s points outside the repo", name)
	}
	return real, nil
}

func Podman(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdout = &stdout
	err := RunCmd(cmd)
	return strings.TrimSpace(stdout.String()), err
}

func Run(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return RunCmd(cmd)
}

// runCmd runs cmd and folds the tail of stderr into the error, so failures are readable in the UI.
func RunCmd(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(cmd.Path), cmd.Args[1], err, msg)
	}
	return nil
}

func Journal(ctx context.Context, container string, lines int) string {
	out, _ := exec.CommandContext(ctx, "journalctl", "--user", "-u", container+".service", "-n", strconv.Itoa(lines), "--no-pager", "-o", "cat").Output()
	return string(out)
}

// ensureNetwork creates a network the first time.
func EnsureNetwork(ctx context.Context, name string) error {
	if _, err := Podman(ctx, "network", "exists", name); err == nil {
		return nil
	}
	_, err := Podman(ctx, "network", "create", name)
	return err
}

// PodmanVersion returns the podman client version, or "" if podman isn't available.
func PodmanVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "podman", "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
