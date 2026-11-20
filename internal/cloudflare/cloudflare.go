// Package cloudflare is the little of Cloudflare's API walroos needs: zones, remote-managed
// tunnels and their ingress, and the CNAMEs that point a domain at a tunnel.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const API = "https://api.cloudflare.com/client/v4"

// ErrRecordExists: the name already has a DNS record walroos didn't make. Never overwritten.
var ErrRecordExists = errors.New("walroos doesn't overwrite it; remove it in Cloudflare first")

var ErrForbidden = errors.New("the Cloudflare token lacks a permission: it needs Zone · DNS · Edit, Zone · Zone · Read and Account · Cloudflare Tunnel · Edit")

type Client struct {
	Token string
	URL   string // API, or a fake in tests
	HTTP  *http.Client
}

func New(token, apiURL string) *Client {
	if apiURL == "" {
		apiURL = API
	}
	return &Client{Token: token, URL: apiURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type Zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Rule is one ingress entry of a tunnel: requests for Hostname go to Service.
type Rule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	rd := bytes.NewReader(nil)
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer resp.Body.Close()
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare %s %s: %d, not JSON", method, path, resp.StatusCode)
	}
	if !env.Success {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%s (%d)", e.Message, e.Code))
		}
		err := fmt.Errorf("cloudflare %s %s: %d %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, strings.Join(msgs, "; "))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w (%v)", ErrForbidden, err)
		}
		return err
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// Zones lists the zones the token can see. ponytail: first page (50 zones).
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var zones []Zone
	return zones, c.do(ctx, http.MethodGet, "/zones?per_page=50", nil, &zones)
}

// Tunnel returns the id of the remote-managed tunnel called name, creating it if needed.
func (c *Client) Tunnel(ctx context.Context, account, name string) (string, error) {
	var found []struct{ ID string }
	q := url.Values{"name": {name}, "is_deleted": {"false"}}
	if err := c.do(ctx, http.MethodGet, "/accounts/"+account+"/cfd_tunnel?"+q.Encode(), nil, &found); err != nil {
		return "", err
	}
	if len(found) > 0 {
		return found[0].ID, nil
	}
	var t struct{ ID string }
	err := c.do(ctx, http.MethodPost, "/accounts/"+account+"/cfd_tunnel", map[string]string{"name": name, "config_src": "cloudflare"}, &t)
	return t.ID, err
}

// TunnelToken is what `cloudflared tunnel run` needs to serve the tunnel.
func (c *Client) TunnelToken(ctx context.Context, account, id string) (string, error) {
	var token string
	return token, c.do(ctx, http.MethodGet, "/accounts/"+account+"/cfd_tunnel/"+id+"/token", nil, &token)
}

// PutIngress replaces the tunnel's ingress; anything else gets a 404.
func (c *Client) PutIngress(ctx context.Context, account, id string, rules []Rule) error {
	rules = append(append([]Rule{}, rules...), Rule{Service: "http_status:404"})
	body := map[string]any{"config": map[string]any{"ingress": rules}}
	return c.do(ctx, http.MethodPut, "/accounts/"+account+"/cfd_tunnel/"+id+"/configurations", body, nil)
}

func (c *Client) records(ctx context.Context, zone, name string) ([]Record, error) {
	var recs []Record
	return recs, c.do(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?"+url.Values{"name": {name}}.Encode(), nil, &recs)
}

// Target is the CNAME content that routes a hostname to a tunnel.
func Target(tunnel string) string { return tunnel + ".cfargotunnel.com" }

// PointAt makes name a proxied CNAME to the tunnel. It replaces a CNAME to some tunnel (the
// app moved nodes) but never another record: that's someone else's.
func (c *Client) PointAt(ctx context.Context, zone, name, tunnel string) error {
	recs, err := c.records(ctx, zone, name)
	if err != nil {
		return err
	}
	body := map[string]any{"type": "CNAME", "name": name, "content": Target(tunnel), "proxied": true, "comment": "walroos"}
	for _, r := range recs {
		if r.Type != "CNAME" || !strings.HasSuffix(r.Content, ".cfargotunnel.com") {
			return fmt.Errorf("%s already has a %s record (%s): %w", name, r.Type, r.Content, ErrRecordExists)
		}
		if r.Content == Target(tunnel) {
			return nil
		}
		return c.do(ctx, http.MethodPut, "/zones/"+zone+"/dns_records/"+r.ID, body, nil)
	}
	return c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", body, nil)
}

// Unpoint removes name's CNAME if it still points at the tunnel.
func (c *Client) Unpoint(ctx context.Context, zone, name, tunnel string) error {
	recs, err := c.records(ctx, zone, name)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Type == "CNAME" && r.Content == Target(tunnel) {
			if err := c.do(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+r.ID, nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ZoneFor returns the zone that holds host: the longest zone name it ends with.
func ZoneFor(host string, zones []Zone) (Zone, bool) {
	var best Zone
	for _, z := range zones {
		if (host == z.Name || strings.HasSuffix(host, "."+z.Name)) && len(z.Name) > len(best.Name) {
			best = z
		}
	}
	return best, best.ID != ""
}
