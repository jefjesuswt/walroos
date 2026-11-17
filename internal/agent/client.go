package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

var ErrNoAddress = errors.New("node has no tailnet address yet (no heartbeat received)")

// Client is how the hub calls node agents. HTTP must dial over the tailnet (tsnet's HTTPClient).
type Client struct {
	HTTP *http.Client
}

// Info calls GET /v1/info on the agent at address (a tailnet IP, or host:port in tests).
func (c *Client) Info(ctx context.Context, address, tokenHash string) (Info, error) {
	var info Info
	return info, c.get(ctx, address, tokenHash, "/v1/info", &info)
}

func (c *Client) get(ctx context.Context, address, tokenHash, path string, out any) error {
	if address == "" {
		return ErrNoAddress
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, Port)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tokenHash)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("agent %s: %d %s", address, resp.StatusCode, e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
