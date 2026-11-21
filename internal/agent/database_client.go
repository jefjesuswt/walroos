package agent

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

func (c *Client) ListDatabases(ctx context.Context, address, tokenHash string) ([]Database, error) {
	var list []Database
	return list, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/databases", nil, &list)
}

func (c *Client) CreateDatabase(ctx context.Context, address, tokenHash string, spec DatabaseSpec) (Database, error) {
	var d Database
	return d, c.do(ctx, http.MethodPost, address, tokenHash, "/v1/databases", spec, &d)
}

func (c *Client) DeleteDatabase(ctx context.Context, address, tokenHash, name string, purge bool) error {
	q := ""
	if purge {
		q = "?purge=1"
	}
	return c.do(ctx, http.MethodDelete, address, tokenHash, "/v1/databases/"+name+q, nil, nil)
}

// BackupDatabase and RestoreDatabase take as long as the dump: they ignore the client's
// timeout and rely on ctx.
func (c *Client) BackupDatabase(ctx context.Context, address, tokenHash, name string, keep int) (Backup, error) {
	var b Backup
	return b, c.doLong(ctx, http.MethodPost, address, tokenHash, "/v1/databases/"+name+"/backups", map[string]int{"keep": keep}, &b)
}

func (c *Client) ListBackups(ctx context.Context, address, tokenHash, name string) ([]Backup, error) {
	var list []Backup
	return list, c.do(ctx, http.MethodGet, address, tokenHash, "/v1/databases/"+name+"/backups", nil, &list)
}

func (c *Client) RestoreDatabase(ctx context.Context, address, tokenHash, name, file string) error {
	return c.doLong(ctx, http.MethodPost, address, tokenHash, "/v1/databases/"+name+"/restore", map[string]string{"file": file}, nil)
}

// DownloadBackup returns the dump's body; the caller closes it.
func (c *Client) DownloadBackup(ctx context.Context, address, tokenHash, name, file string) (io.ReadCloser, int64, error) {
	hc := *c.HTTP
	hc.Timeout = 0
	resp, err := c.send(ctx, &hc, http.MethodGet, address, tokenHash, "/v1/databases/"+name+"/backups/"+url.PathEscape(file), nil)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.ContentLength, nil
}

func (c *Client) doLong(ctx context.Context, method, address, tokenHash, path string, body, out any) error {
	long := &Client{HTTP: &http.Client{Transport: c.HTTP.Transport}}
	return long.do(ctx, method, address, tokenHash, path, body, out)
}
