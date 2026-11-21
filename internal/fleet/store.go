package fleet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
)

// ErrNodeHasWorkspaces: foreign keys keep what runs on a node from being orphaned.
var ErrNodeHasWorkspaces = errors.New("the node still has workspaces, apps or databases; delete them first")

type Store struct {
	db *config.Database
}

func NewStore(db *config.Database) *Store {
	return &Store{db: db}
}

func (s *Store) SaveJoinToken(ctx context.Context, hash string, expiresAt time.Time) error {
	_, err := s.db.Write.ExecContext(ctx, `INSERT INTO join_tokens (token_hash, expires_at) VALUES (?, ?)`, hash, expiresAt)
	return err
}

// Join consumes a join token and creates the node in one transaction,
// so a failed insert (e.g. duplicate name) doesn't burn the token.
func (s *Store) Join(ctx context.Context, joinHash string, n Node, tokenHash string) error {
	tx, err := s.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM join_tokens WHERE token_hash = ? AND expires_at > ?`, joinHash, time.Now().UTC())
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows != 1 {
		return ErrInvalidJoinToken
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO nodes (id, name, roles, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		n.ID, n.Name, strings.Join(n.Roles, ","), tokenHash, n.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: nodes.name") {
			return ErrNodeNameAlreadyExists
		}
		return fmt.Errorf("insert node: %w", err)
	}

	return tx.Commit()
}

// Heartbeat records telemetry for the node whose id AND token hash match.
func (s *Store) Heartbeat(ctx context.Context, id, tokenHash, address string, t Telemetry) error {
	body, err := json.Marshal(t)
	if err != nil {
		return err
	}

	res, err := s.db.Write.ExecContext(ctx,
		`UPDATE nodes SET telemetry = ?, address = ?, last_seen = ? WHERE id = ? AND token_hash = ?`,
		string(body), address, time.Now().UTC(), id, tokenHash,
	)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows != 1 {
		return ErrUnauthorized // unknown id or wrong token: don't tell which
	}
	return nil
}

func (s *Store) Authenticate(ctx context.Context, id, tokenHash string) error {
	var one int
	err := s.db.Read.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id = ? AND token_hash = ?`, id, tokenHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	return err
}

// AgentTarget returns what the hub needs to call a node's agent: its last tailnet IP and token hash.
func (s *Store) AgentTarget(ctx context.Context, id string) (address, tokenHash string, err error) {
	err = s.db.Read.QueryRowContext(ctx, `SELECT address, token_hash FROM nodes WHERE id = ?`, id).Scan(&address, &tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNodeNotFound
	}
	return address, tokenHash, err
}

func (s *Store) List(ctx context.Context) ([]Node, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, name, roles, address, telemetry, created_at, last_seen FROM nodes ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	nodes := []Node{}
	for rows.Next() {
		var (
			n         Node
			roles     string
			telemetry string
			lastSeen  sql.NullTime
		)
		if err := rows.Scan(&n.ID, &n.Name, &roles, &n.Address, &telemetry, &n.CreatedAt, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		n.Roles = strings.Split(roles, ",")
		if err := json.Unmarshal([]byte(telemetry), &n.Telemetry); err != nil {
			return nil, fmt.Errorf("decode telemetry of %s: %w", n.Name, err)
		}
		if lastSeen.Valid {
			n.LastSeen = &lastSeen.Time
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.Write.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return ErrNodeHasWorkspaces
		}
		return fmt.Errorf("delete node: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return ErrNodeNotFound
	}
	return nil
}
