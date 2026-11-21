package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/jefjesuswt/walroos/internal/cloudflare"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

var (
	ErrNoCloudflare = whttp.Conflict("Cloudflare isn't set up: add an API token in Settings")
	ErrNoZones      = whttp.Invalid("the token sees no zone; give it Zone:Read and DNS:Edit on your domain")
)

const cloudflareKey = "cloudflare_token"

// CloudflareStatus is what the UI shows: whether a token is saved and the zones it can edit.
type CloudflareStatus struct {
	Configured bool     `json:"configured"`
	Zones      []string `json:"zones"`
}

// SetCloudflare stores the API token (sealed) once it has proven to see at least one zone.
// It needs Zone:Read and DNS:Edit on the zones, and Cloudflare Tunnel:Edit on the account.
func (s *Service) SetCloudflare(ctx context.Context, token string) (CloudflareStatus, error) {
	token, sealed, err := s.seal(token)
	if err != nil {
		return CloudflareStatus{}, err
	}
	zones, err := cloudflare.New(token, s.CloudflareAPI).Zones(ctx)
	if err != nil {
		return CloudflareStatus{}, err
	}
	if len(zones) == 0 {
		return CloudflareStatus{}, ErrNoZones
	}
	if err := s.set(ctx, map[string]string{cloudflareKey: sealed}); err != nil {
		return CloudflareStatus{}, err
	}
	return zoneStatus(zones), nil
}

// Cloudflare reports the saved token's zones, asking Cloudflare.
func (s *Service) Cloudflare(ctx context.Context) (CloudflareStatus, error) {
	cf, err := s.CloudflareClient(ctx)
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
	return zoneStatus(zones), nil
}

// CloudflareClient is a client with the saved token, or ErrNoCloudflare.
func (s *Service) CloudflareClient(ctx context.Context) (*cloudflare.Client, error) {
	token, err := s.token(ctx, cloudflareKey)
	if err != nil {
		return nil, fmt.Errorf("cloudflare token: %w", err)
	}
	if token == "" {
		return nil, ErrNoCloudflare
	}
	return cloudflare.New(token, s.CloudflareAPI), nil
}

func zoneStatus(zones []cloudflare.Zone) CloudflareStatus {
	st := CloudflareStatus{Configured: true, Zones: []string{}}
	for _, z := range zones {
		st.Zones = append(st.Zones, z.Name)
	}
	return st
}
