package quadlet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/host"
)

// Apps are reachable from the internet, so assume one gets compromised: it must not become a
// way into the owner's LAN, the host or the tailnet. Rootless podman forwards app traffic in
// its rootless network namespace, where a rule on the app networks' bridges rejects private
// destinations unless they stay on those bridges (br_netfilter shows bridged traffic to this
// hook too). Public egress, DNS (aardvark is local to the netns) and app ↔ app traffic are
// untouched. Workspaces (walroos-dev) are the owner's own code and keep LAN access.
//
// The netns is torn down when no container runs and rebuilt without the rule, so every app and
// tunnel unit applies it right after starting (ExecStartPost). If that fails, the container is
// SIGKILLed on the spot and the unit fails. A plain failing ExecStartPost isn't enough: systemd
// then stops the container gracefully, and an app that ignores SIGTERM ran unguarded for ~50 s
// (and again on every Restart=always cycle).
//
// ponytail: between the container starting and the rule landing (milliseconds, and only when
// the netns was just rebuilt) the app is unguarded.
//
// ponytail: no per-app allowlist; add one when an app must reach a LAN service (a NAS database).
const GuardFile = "walroos-guard.nft"

var guardBlocked = [...]string{
	"10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10", // LAN, link-local (cloud metadata), CGNAT + tailnet
	"fc00::/7, fe80::/10",
}

var bridgeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`) // goes into the nft file

// guard writes the rule for the app networks and returns the unit line that applies it to container.
func (h *Host) Guard(ctx context.Context, container string) (string, error) {
	nets := []string{agent.AppNetwork, EdgeNetwork}
	for _, n := range nets {
		if err := host.EnsureNetwork(ctx, n); err != nil {
			return "", err
		}
	}
	// Every project's network too: a project's services get the same rule as the rest.
	projects, err := host.Podman(ctx, "network", "ls", "--filter", "label="+LabelProject, "--format", "{{.Name}}")
	if err != nil {
		return "", err
	}
	nets = append(nets, strings.Fields(projects)...)
	out, err := host.Podman(ctx, append(append([]string{"network", "inspect"}, nets...), "--format", "{{.NetworkInterface}}")...)
	if err != nil {
		return "", err
	}
	var bridges []string
	for _, b := range strings.Fields(out) {
		if !bridgeRe.MatchString(b) {
			return "", fmt.Errorf("unexpected bridge name %q", b)
		}
		bridges = append(bridges, `"`+b+`"`)
	}
	if len(bridges) != len(nets) {
		return "", fmt.Errorf("app network bridges: %q", out)
	}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return "", err
	}
	f := filepath.Join(h.Dir, GuardFile)
	if err := os.WriteFile(f, []byte(guardRules(strings.Join(bridges, ", "))), 0o644); err != nil {
		return "", err
	}
	return guardExec(f, container)
}

var guardArgRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`) // no quotes, %, $ or spaces: systemd would read them

// guardExec is the ExecStartPost line: apply the rule, or kill the container right away.
// "$$" is a literal "$" for systemd, so the shell sees "$0" and "$1".
func guardExec(file, container string) (string, error) {
	if !guardArgRe.MatchString(file) || !guardArgRe.MatchString(container) {
		return "", fmt.Errorf("guard: unsafe path or container name %q, %q", file, container)
	}
	return `ExecStartPost=/bin/sh -c 'podman unshare --rootless-netns nft -f "$$0" || { podman kill --signal KILL "$$1"; exit 1; }' ` + file + " " + container, nil
}

// guardRules replaces the table in one transaction: declaring it first makes the delete safe.
func guardRules(bridges string) string {
	return fmt.Sprintf(`# Written by walroos: apps can't reach private networks. Don't edit.
table inet walroos_guard
delete table inet walroos_guard
table inet walroos_guard {
	chain forward {
		type filter hook forward priority -10; policy accept;
		iifname { %[1]s } oifname != { %[1]s } ip daddr { %[2]s } reject with icmpx admin-prohibited
		iifname { %[1]s } oifname != { %[1]s } ip6 daddr { %[3]s } reject with icmpx admin-prohibited
	}
}
`, bridges, guardBlocked[0], guardBlocked[1])
}
