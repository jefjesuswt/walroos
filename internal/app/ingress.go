package app

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/jefjesuswt/walroos/internal/cloudflare"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrDomain      = whttp.Invalid("domains must be hostnames like app.example.com")
	ErrDomainTaken = whttp.Conflict("that domain already routes to another app")
	ErrNoZone      = whttp.Invalid("not in a zone this Cloudflare token can edit")
)

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// SetDomains routes exactly these hostnames to the app: ingress rules on its node's tunnel
// (created, and cloudflared started, the first time) and proxied CNAMEs to it.
func (s *Service) SetDomains(ctx context.Context, name string, domains []string) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	want, err := normalizeDomains(domains)
	if err != nil {
		return App{}, err
	}
	if a.Port == 0 && len(want) > 0 {
		return App{}, ErrNoPort
	}
	cf, zones, err := s.zonesFor(ctx, want)
	if err != nil {
		return App{}, err
	}
	all, err := s.store.domains(ctx)
	if err != nil {
		return App{}, err
	}
	old := all[a.ID]
	if err := s.store.replaceDomains(ctx, a.ID, want); err != nil {
		return App{}, err
	}
	// ponytail: on a Cloudflare error the old domains come back, but records already changed stay.
	if err := s.route(ctx, cf, zones, a.NodeID, want, old); err != nil {
		if rerr := s.store.replaceDomains(context.WithoutCancel(ctx), a.ID, old); rerr != nil {
			slog.Error("domains: restore after a Cloudflare error", "app", a.Name, "err", rerr)
		}
		return App{}, err
	}
	return s.Get(ctx, name)
}

// normalizeDomains lowercases and dedupes hostnames, refusing anything that isn't one.
func normalizeDomains(domains []string) ([]string, error) {
	var want []string
	for _, d := range domains {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if len(d) > 253 || !hostRe.MatchString(d) {
			return nil, fmt.Errorf("%w: %q", ErrDomain, d)
		}
		if !slices.Contains(want, d) {
			want = append(want, d)
		}
	}
	return want, nil
}

// zonesFor returns the Cloudflare client and its zones, checking each domain is in one of them.
func (s *Service) zonesFor(ctx context.Context, domains []string) (*cloudflare.Client, []cloudflare.Zone, error) {
	cf, err := s.settings.CloudflareClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	zones, err := cf.Zones(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range domains {
		if _, ok := cloudflare.ZoneFor(d, zones); !ok {
			return nil, nil, fmt.Errorf("%s: %w", d, ErrNoZone)
		}
	}
	return cf, zones, nil
}

// route brings the node's tunnel in line with the database: tunnel and cloudflared up, the
// ingress rules, then DNS for the added and removed domains.
func (s *Service) route(ctx context.Context, cf *cloudflare.Client, zones []cloudflare.Zone, nodeID string, added, removed []string) error {
	address, tokenHash, err := s.nodes.AgentTarget(ctx, nodeID) // before creating a tunnel for a node we can't reach
	if err != nil {
		return err
	}
	t, err := s.ensureTunnel(ctx, cf, zones, nodeID)
	if err != nil {
		return err
	}
	if err := s.startCloudflared(ctx, cf, t, address, tokenHash); err != nil {
		return err
	}
	rules, err := s.store.nodeRules(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := cf.PutIngress(ctx, t.AccountID, t.ID, rules); err != nil {
		return err
	}
	return pointDNS(ctx, cf, zones, t.ID, added, removed)
}

// ensureTunnel returns the node's tunnel, creating it the first time.
func (s *Service) ensureTunnel(ctx context.Context, cf *cloudflare.Client, zones []cloudflare.Zone, nodeID string) (tunnel, error) {
	t, ok, err := s.store.tunnel(ctx, nodeID)
	if err != nil || ok {
		return t, err
	}
	node, err := s.nodeName(ctx, nodeID)
	if err != nil {
		return t, err
	}
	t.AccountID = zones[0].Account.ID // ponytail: one Cloudflare account
	if t.ID, err = cf.Tunnel(ctx, t.AccountID, "walroos-"+node); err != nil {
		return t, err
	}
	return t, s.store.saveTunnel(ctx, nodeID, t)
}

// startCloudflared hands the node the tunnel's token. Idempotent: a reinstalled node gets it back.
func (s *Service) startCloudflared(ctx context.Context, cf *cloudflare.Client, t tunnel, address, tokenHash string) error {
	token, err := cf.TunnelToken(ctx, t.AccountID, t.ID)
	if err != nil {
		return err
	}
	if err := s.agents.SetTunnel(ctx, address, tokenHash, token); err != nil {
		return fmt.Errorf("start cloudflared on the node: %w", err)
	}
	return nil
}

// pointDNS points the added domains at the tunnel and unpoints the removed ones.
func pointDNS(ctx context.Context, cf *cloudflare.Client, zones []cloudflare.Zone, tunnelID string, added, removed []string) error {
	for _, d := range added {
		if z, ok := cloudflare.ZoneFor(d, zones); ok {
			if err := cf.PointAt(ctx, z.ID, d, tunnelID); err != nil {
				return err
			}
		}
	}
	for _, d := range removed {
		if z, ok := cloudflare.ZoneFor(d, zones); ok && !slices.Contains(added, d) {
			if err := cf.Unpoint(ctx, z.ID, d, tunnelID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) nodeName(ctx context.Context, id string) (string, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.ID == id {
			return n.Name, nil
		}
	}
	return "", fmt.Errorf("node %s not found", id)
}

// unroute drops a deleted app's domains from its node's tunnel. Best effort: the app is gone
// either way, and a leftover rule or CNAME only answers 502.
func (s *Service) unroute(ctx context.Context, nodeID string, domains []string) {
	if len(domains) == 0 {
		return
	}
	cf, err := s.settings.CloudflareClient(ctx)
	var zones []cloudflare.Zone
	if err == nil {
		zones, err = cf.Zones(ctx)
	}
	if err == nil {
		err = s.route(ctx, cf, zones, nodeID, nil, domains)
	}
	if err != nil {
		slog.Warn("app deleted but its domains are still in Cloudflare", "domains", domains, "err", err)
	}
}
