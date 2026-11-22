package host

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
)

// Problems runs the checks. Each is cheap and best effort: one that can't tell says nothing.
func Problems(ctx context.Context) []agent.Problem {
	var list []agent.Problem
	if KernelUpdated() {
		list = append(list, agent.KernelUpdated)
	}
	if noEgress(ctx) {
		list = append(list, agent.NoEgress)
	}
	return list
}

// KernelUpdated: /usr/lib/modules has kernels, but not the running one.
func KernelUpdated() bool {
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return modulesGone(strings.TrimSpace(string(release)), "/usr/lib/modules", "/lib/modules")
}

func modulesGone(release string, roots ...string) bool {
	others := false
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, release)); err == nil {
			return false
		}
		if dirs, _ := os.ReadDir(root); len(dirs) > 0 {
			others = true
		}
	}
	return others
}

// noEgress: the rootless network namespace exists and has no default route. Joining it doesn't
// create it when no container uses it (then there's nothing to check).
func noEgress(ctx context.Context) bool {
	if os.Geteuid() == 0 {
		return false // rootful podman has no rootless namespace
	}
	out, err := Podman(ctx, "unshare", "--rootless-netns", "cat", "/proc/net/route")
	return err == nil && !hasDefaultRoute(out)
}

// hasDefaultRoute reads /proc/net/route: a default route has destination 00000000.
func hasDefaultRoute(table string) bool {
	sc := bufio.NewScanner(strings.NewReader(table))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 1 && f[1] == "00000000" {
			return true
		}
	}
	return false
}
