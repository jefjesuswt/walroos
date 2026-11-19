package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Apps are reachable from the internet, so assume one gets compromised: it must not become a
// way into the owner's LAN, the host or the tailnet. Rootless podman forwards app traffic in
// its rootless network namespace, where a rule on the app networks' bridges rejects private
// destinations unless they stay on those bridges (br_netfilter shows bridged traffic to this
// hook too). Public egress, DNS (aardvark is local to the netns) and app ↔ app traffic are
// untouched. Workspaces (walroos-dev) are the owner's own code and keep LAN access.
//
// The netns is torn down when no container runs and rebuilt without the rule, so every app and
// tunnel unit applies it right after starting (ExecStartPost). If that fails the unit fails:
// an app never runs without the guard.
//
// ponytail: no per-app allowlist; add one when an app must reach a LAN service (a NAS database).
const guardFile = "walroos-guard.nft"

var guardBlocked = [...]string{
	"10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10", // LAN, link-local (cloud metadata), CGNAT + tailnet
	"fc00::/7, fe80::/10",
}

var bridgeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`) // goes into the nft file

// guard writes the rule for the app networks and returns the unit line that applies it.
func (a *Apps) guard(ctx context.Context) (string, error) {
	for _, n := range []string{AppNetwork, EdgeNetwork} {
		if err := ensureNetwork(ctx, n); err != nil {
			return "", err
		}
	}
	out, err := podman(ctx, "network", "inspect", AppNetwork, EdgeNetwork, "--format", "{{.NetworkInterface}}")
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
	if len(bridges) != 2 {
		return "", fmt.Errorf("app network bridges: %q", out)
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return "", err
	}
	f := filepath.Join(a.dir, guardFile)
	if err := os.WriteFile(f, []byte(guardRules(strings.Join(bridges, ", "))), 0o644); err != nil {
		return "", err
	}
	return "ExecStartPost=podman unshare --rootless-netns nft -f " + f, nil
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
