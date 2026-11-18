package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// AgentError is a non-2xx answer from the agent.
type AgentError struct {
	Status int
	Msg    string
}

func (e *AgentError) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Msg) }

func (c *Client) ListWorkspaces(ctx context.Context, address, tokenHash string) ([]Workspace, error) {
	var list []Workspace
	return list, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/workspaces", nil, &list)
}

func (c *Client) CreateWorkspace(ctx context.Context, address, tokenHash string, spec WorkspaceSpec) (Workspace, error) {
	var ws Workspace
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces", spec, &ws)
}

func (c *Client) GetWorkspace(ctx context.Context, address, tokenHash, name string) (Workspace, error) {
	var ws Workspace
	return ws, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/workspaces/"+name, nil, &ws)
}

func (c *Client) StartWorkspace(ctx context.Context, address, tokenHash, name string) (Workspace, error) {
	var ws Workspace
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces/"+name+"/start", nil, &ws)
}

func (c *Client) StopWorkspace(ctx context.Context, address, tokenHash, name string) (Workspace, error) {
	var ws Workspace
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces/"+name+"/stop", nil, &ws)
}

func (c *Client) RebuildWorkspace(ctx context.Context, address, tokenHash, name string) (Workspace, error) {
	var ws Workspace
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces/"+name+"/rebuild", nil, &ws)
}

func (c *Client) DeleteWorkspace(ctx context.Context, address, tokenHash, name string) error {
	return c.do(ctx, http.MethodDelete, address, tokenHash, "/v1/workspaces/"+name, nil, nil)
}

// Info calls GET /v1/info on the agent at address (a tailnet IP, or host:port in tests).
func (c *Client) Info(ctx context.Context, address, tokenHash string) (Info, error) {
	var info Info
	return info, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/info", nil, &info)
}

func (c *Client) do(ctx context.Context, method, address, tokenHash, path string, body, out any) error {
	if address == "" {
		return ErrNoAddress
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, Port)
	}

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tokenHash)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return &AgentError{Status: resp.StatusCode, Msg: strings.TrimSpace(e.Error)}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
