package config_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jefjesuswt/walroos/config"
)

// TestSchemaConstraints checks that the constraints the stores will rely on are enforced.
func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	db, err := config.NewSQLite(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	exec := func(q string, args ...any) error {
		_, err := db.Write.ExecContext(ctx, q, args...)
		return err
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	workspace := func(id, name, node, state string) error {
		return exec(`INSERT INTO workspaces (id, name, owner, node_id, repo_url, state, created_at) VALUES (?, ?, 'me@github', ?, 'https://github.com/me/app', ?, ?)`,
			id, name, node, state, now)
	}

	mustExec(`INSERT INTO users (login, role, created_at) VALUES ('me@github', 'owner', ?)`, now)
	mustExec(`INSERT INTO nodes (id, name, roles, token_hash, created_at) VALUES ('n1', 'homelab-1', 'dev', 'h1', ?)`, now)

	if err := workspace("w1", "app", "n1", "creating"); err != nil {
		t.Fatal("valid workspace:", err)
	}
	if workspace("w2", "app", "n1", "creating") == nil {
		t.Error("duplicate workspace name was accepted")
	}
	if workspace("w3", "other", "missing-node", "creating") == nil {
		t.Error("workspace on an unknown node was accepted (foreign keys off?)")
	}
	if workspace("w4", "other", "n1", "exploded") == nil {
		t.Error("unknown workspace state was accepted")
	}
	if exec(`DELETE FROM nodes WHERE id = 'n1'`) == nil {
		t.Error("removed a node that still has workspaces")
	}

	key := func(id, name, fp string) error {
		return exec(`INSERT INTO ssh_keys (id, login, name, public_key, fingerprint, created_at) VALUES (?, 'me@github', ?, 'ssh-ed25519 AAAA', ?, ?)`, id, name, fp, now)
	}
	if err := key("k1", "laptop", "SHA256:aaa"); err != nil {
		t.Fatal("valid key:", err)
	}
	if key("k2", "desktop", "SHA256:aaa") == nil {
		t.Error("same key fingerprint added twice")
	}
	if key("k3", "laptop", "SHA256:bbb") == nil {
		t.Error("duplicate key name for the same user")
	}

	mustExec(`DELETE FROM workspaces`)
	mustExec(`DELETE FROM users WHERE login = 'me@github'`)
	var keys int
	db.Read.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_keys`).Scan(&keys)
	if keys != 0 {
		t.Errorf("deleting a user left %d ssh keys behind", keys)
	}

	// 010: three fixed roles; the old 'member' is gone. Rebuilding users kept the keys' and
	// workspaces' foreign keys pointing at it: deleting a user still cascades to their keys.
	for role, ok := range map[string]bool{"owner": true, "deployer": true, "developer": true, "member": false, "admin": false} {
		if err := exec(`INSERT INTO users (login, role, created_at) VALUES (?, ?, ?)`, role+"@github", role, now); (err == nil) != ok {
			t.Errorf("role %q accepted = %v, want %v", role, err == nil, ok)
		}
	}
	mustExec(`INSERT INTO ssh_keys (id, login, name, public_key, fingerprint, created_at) VALUES ('kd', 'deployer@github', 'd', 'ssh-ed25519 AAAA', 'SHA256:dep', ?)`, now)
	mustExec(`DELETE FROM users WHERE login = 'deployer@github'`)
	var left int
	db.Read.QueryRowContext(ctx, `SELECT count(*) FROM ssh_keys WHERE login = 'deployer@github'`).Scan(&left)
	if left != 0 {
		t.Error("deleting a user left their SSH keys: the cascade is gone")
	}
	if exec(`INSERT INTO ssh_keys (id, login, name, public_key, fingerprint, created_at) VALUES ('kx', 'ghost@github', 'x', 'ssh-ed25519 AAAA', 'SHA256:ghost', ?)`, now) == nil {
		t.Error("a key for an unknown user was accepted (foreign keys off after migrating?)")
	}
}
