package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/jefjesuswt/walroos/internal/cloudflare"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNoCloudflare = whttp.Conflict("Cloudflare isn't set up: add an API token in Settings")
	ErrDomain       = whttp.Invalid("domains must be hostnames like app.example.com")
	ErrDomainTaken  = whttp.Conflict("that domain already routes to another app")
	ErrNoZone       = whttp.Invalid("not in a zone this Cloudflare token can edit")
)

// CloudflareStatus is what the UI shows; the token itself never leaves the hub.
type CloudflareStatus struct {
	Configured bool     `json:"configured"`
	Zones      []string `json:"zones"`
}

const cloudflareKey = "cloudflare_token"

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// SetCloudflare stores the API token (sealed) once it has proven to see at least one zone.
// It needs Zone:Read and DNS:Edit on the zones, and Cloudflare Tunnel:Edit on the account.
func (s *Service) SetCloudflare(ctx context.Context, token string) (CloudflareStatus, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return CloudflareStatus{}, fmt.Errorf("%w: empty token", ErrInvalid)
	}
	zones, err := cloudflare.New(token, s.CloudflareAPI).Zones(ctx)
	if err != nil {
		return CloudflareStatus{}, err
	}
	if len(zones) == 0 {
		return CloudflareStatus{}, fmt.Errorf("%w: the token sees no zone; give it Zone:Read and DNS:Edit on your domain", ErrInvalid)
	}
	sealed, err := s.box.Seal(token)
	if err != nil {
		return CloudflareStatus{}, err
	}
	if err := s.store.setSetting(ctx, cloudflareKey, sealed); err != nil {
		return CloudflareStatus{}, err
	}
	return status(zones), nil
}

func (s *Service) Cloudflare(ctx context.Context) (CloudflareStatus, error) {
	cf, err := s.cloudflare(ctx)
	if errors.Is(err, ErrNoCloudflare) {
		return CloudflareStatus{Zones: []string{}}, nil
	}
	if err != nil {
		return CloudflareStatus{}, err
	}
	zones, err := cf.Zones(ctx)
	if err != nil {
		return CloudflareStatus{Configured: true, Zones: []string{}}, err
	}
	return status(zones), nil
}

func status(zones []cloudflare.Zone) CloudflareStatus {
	st := CloudflareStatus{Configured: true, Zones: []string{}}
	for _, z := range zones {
		st.Zones = append(st.Zones, z.Name)
	}
	return st
}

func (s *Service) cloudflare(ctx context.Context) (*cloudflare.Client, error) {
	sealed, err := s.store.setting(ctx, cloudflareKey)
	if err != nil {
		return nil, err
	}
	if sealed == "" {
		return nil, ErrNoCloudflare
	}
	token, err := s.box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("cloudflare token: %w", err)
	}
	return cloudflare.New(token, s.CloudflareAPI), nil
}

// SetDomains routes exactly these hostnames to the app: ingress rules on its node's tunnel
// (created, and cloudflared started, the first time) and proxied CNAMEs to it.
func (s *Service) SetDomains(ctx context.Context, name string, domains []string) (App, error) {
	a, err := s.store.Get(ctx, name)
	if err != nil {
		return App{}, err
	}
	var want []string
	for _, d := range domains {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if len(d) > 253 || !hostRe.MatchString(d) {
			return App{}, fmt.Errorf("%w: %q", ErrDomain, d)
		}
		if !slices.Contains(want, d) {
			want = append(want, d)
		}
	}
	cf, err := s.cloudflare(ctx)
	if err != nil {
		return App{}, err
	}
	zones, err := cf.Zones(ctx)
	if err != nil {
		return App{}, err
	}
	for _, d := range want {
		if _, ok := cloudflare.ZoneFor(d, zones); !ok {
			return App{}, fmt.Errorf("%s: %w", d, ErrNoZone)
		}
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

// route brings the node's tunnel in line with the database: tunnel and cloudflared up, the
// ingress rules, then DNS for the added and removed domains.
func (s *Service) route(ctx context.Context, cf *cloudflare.Client, zones []cloudflare.Zone, nodeID string, added, removed []string) error {
	t, ok, err := s.store.tunnel(ctx, nodeID)
	if err != nil {
		return err
	}
	address, tokenHash, err := s.nodes.AgentTarget(ctx, nodeID)
	if err != nil {
		return err
	}
	if !ok {
		node, err := s.nodeName(ctx, nodeID)
		if err != nil {
			return err
		}
		t.AccountID = zones[0].Account.ID // ponytail: one Cloudflare account
		if t.ID, err = cf.Tunnel(ctx, t.AccountID, "walroos-"+node); err != nil {
			return err
		}
		if err := s.store.saveTunnel(ctx, nodeID, t); err != nil {
			return err
		}
	}
	token, err := cf.TunnelToken(ctx, t.AccountID, t.ID)
	if err != nil {
		return err
	}
	if err := s.agents.SetTunnel(ctx, address, tokenHash, token); err != nil { // idempotent: a reinstalled node gets it back
		return fmt.Errorf("start cloudflared on the node: %w", err)
	}
	rules, err := s.store.nodeRules(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := cf.PutIngress(ctx, t.AccountID, t.ID, rules); err != nil {
		return err
	}
	for _, d := range added {
		if z, ok := cloudflare.ZoneFor(d, zones); ok {
			if err := cf.PointAt(ctx, z.ID, d, t.ID); err != nil {
				return err
			}
		}
	}
	for _, d := range removed {
		if z, ok := cloudflare.ZoneFor(d, zones); ok && !slices.Contains(added, d) {
			if err := cf.Unpoint(ctx, z.ID, d, t.ID); err != nil {
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
	cf, err := s.cloudflare(ctx)
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
