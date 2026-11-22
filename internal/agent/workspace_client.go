package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// AgentError is a non-2xx answer from the agent.
type AgentError struct {
	Status int
	Msg    string
}

func (e *AgentError) Error() string { return fmt.Sprintf("%d: %s", e.Status, e.Msg) }

// HTTPStatus: the hub answers 502 when a node refuses or fails.
func (e *AgentError) HTTPStatus() int { return http.StatusBadGateway }

func (c *Client) ListWorkspaces(ctx context.Context, address, tokenHash string) ([]Workspace, error) {
	var list []Workspace
	return list, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/workspaces", nil, &list)
}

func (c *Client) CreateWorkspace(ctx context.Context, address, tokenHash string, spec WorkspaceSpec, env []string) (Workspace, error) {
	var ws Workspace
	body := struct {
		WorkspaceSpec
		Env []string `json:"env,omitempty"`
	}{spec, env}
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces", body, &ws)
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

func (c *Client) RebuildWorkspace(ctx context.Context, address, tokenHash, name string, env []string) (Workspace, error) {
	var ws Workspace
	body := struct {
		Env []string `json:"env,omitempty"`
	}{env}
	return ws, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/workspaces/"+name+"/rebuild", body, &ws)
}

func (c *Client) DeleteWorkspace(ctx context.Context, address, tokenHash, name string) error {
	return c.do(ctx, http.MethodDelete, address, tokenHash, "/v1/workspaces/"+name, nil, nil)
}

func (c *Client) Detect(ctx context.Context, address, tokenHash, repoURL, branch, gitToken string) (json.RawMessage, error) {
	var d json.RawMessage // passed through to the UI as is
	body := map[string]string{"repo_url": repoURL, "branch": branch, "git_token": gitToken}
	return d, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/detect", body, &d)
}

// RepoFiles reads files from a repo on the node at address (a shallow clone it then forgets):
// the paths that exist, by path.
func (c *Client) RepoFiles(ctx context.Context, address, tokenHash string, in RepoFilesRequest) (map[string]string, error) {
	var out struct{ Files map[string]string }
	return out.Files, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/repo/files", in, &out)
}

// Info calls GET /v1/info on the agent at address (a tailnet IP, or host:port in tests).
func (c *Client) Info(ctx context.Context, address, tokenHash string) (Info, error) {
	var info Info
	return info, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/info", nil, &info)
}

func (c *Client) do(ctx context.Context, method, address, tokenHash, path string, body, out any) error {
	resp, err := c.send(ctx, c.HTTP, method, address, tokenHash, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if b, ok := out.(*string); ok {
		raw, err := io.ReadAll(resp.Body)
		*b = string(raw)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// send makes the call and turns a non-2xx answer into an *AgentError. The caller closes the body.
func (c *Client) send(ctx context.Context, hc *http.Client, method, address, tokenHash, path string, body any) (*http.Response, error) {
	if address == "" {
		return nil, ErrNoAddress
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, Port)
	}

	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tokenHash)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return nil, &AgentError{Status: resp.StatusCode, Msg: strings.TrimSpace(e.Error)}
	}
	return resp, nil
}

func (c *Client) ListApps(ctx context.Context, address, tokenHash string) ([]App, error) {
	var list []App
	return list, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/apps", nil, &list)
}

func (c *Client) AppLogs(ctx context.Context, address, tokenHash, name string, lines int) (string, error) {
	var logs string
	return logs, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/apps/"+name+"/logs?lines="+strconv.Itoa(lines), nil, &logs)
}

// DeleteApp removes the app from the node; purge also removes its named volumes.
func (c *Client) DeleteApp(ctx context.Context, address, tokenHash, name string, purge bool) error {
	q := ""
	if purge {
		q = "?purge=1"
	}
	return c.do(ctx, http.MethodDelete, address, tokenHash, "/v1/apps/"+name+q, nil, nil)
}

// PowerApp stops, starts or restarts the app's running release (AppStop, AppStart, AppRestart).
func (c *Client) PowerApp(ctx context.Context, address, tokenHash, name, action string) error {
	return c.do(ctx, http.MethodPost, address, tokenHash, "/v1/apps/"+name+"/power", map[string]string{"action": action}, nil)
}

// DeployApp runs a deploy on the node, calling onLog with each line of output as it comes.
// It ignores c.HTTP's timeout (a build takes minutes); ctx bounds it.
func (c *Client) DeployApp(ctx context.Context, address, tokenHash string, spec AppSpec, onLog func(string)) (Release, error) {
	return c.streamApp(ctx, address, tokenHash, "deploy", spec, onLog)
}

// SetTunnel makes the node run cloudflared for the tunnel token.
func (c *Client) SetTunnel(ctx context.Context, address, tokenHash, token string) error {
	return c.do(ctx, http.MethodPut, address, tokenHash, "/v1/tunnel", map[string]string{"token": token}, nil)
}

// BuildApp builds on a build node and pushes to its registry, streaming like DeployApp.
func (c *Client) BuildApp(ctx context.Context, address, tokenHash string, spec AppSpec, onLog func(string)) (Release, error) {
	return c.streamApp(ctx, address, tokenHash, "build", spec, onLog)
}

func (c *Client) streamApp(ctx context.Context, address, tokenHash, op string, spec AppSpec, onLog func(string)) (Release, error) {
	hc := *c.HTTP
	hc.Timeout = 0
	resp, err := c.send(ctx, &hc, http.MethodPost, address, tokenHash, "/v1/apps/"+spec.Name+"/"+op, spec)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var line DeployLine
		if err := dec.Decode(&line); err != nil {
			return Release{}, fmt.Errorf("%s stream ended early: %w", op, err)
		}
		switch {
		case line.Release != nil:
			return *line.Release, nil
		case line.Error != "":
			return Release{}, errors.New(line.Error)
		default:
			onLog(line.Log)
		}
	}
}
