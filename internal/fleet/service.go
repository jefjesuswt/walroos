package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"slices"
	"time"

	"github.com/jefjesuswt/walroos/internal/agent"
)

type Service struct {
	store *Store
	now   func() time.Time
}

func NewService(store *Store) *Service {
	return &Service{store: store, now: time.Now}
}

// CreateJoinToken returns a single-use token valid for one hour. Only its hash is stored.
func (s *Service) CreateJoinToken(ctx context.Context) (token string, expiresAt time.Time, err error) {
	token = "wlr_join_" + randomHex(16)
	expiresAt = s.now().UTC().Add(joinTokenTTL)
	return token, expiresAt, s.store.SaveJoinToken(ctx, hash(token), expiresAt)
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Join trades a join token for a node identity. The returned node token is shown once.
func (s *Service) Join(ctx context.Context, joinToken, name string, roles []string) (Node, string, error) {
	if !nameRe.MatchString(name) {
		return Node{}, "", ErrInvalidName
	}
	roles, err := normalizeRoles(roles)
	if err != nil {
		return Node{}, "", err
	}

	n := Node{
		ID:        randomHex(8),
		Name:      name,
		Roles:     roles,
		CreatedAt: s.now().UTC(),
	}
	n.setStatus(s.now())

	nodeToken := "wlr_node_" + randomHex(32)
	if err := s.store.Join(ctx, hash(joinToken), n, hash(nodeToken)); err != nil {
		return Node{}, "", err
	}
	return n, nodeToken, nil
}

func (s *Service) Heartbeat(ctx context.Context, id, nodeToken, address string, t Telemetry) error {
	return s.store.Heartbeat(ctx, id, hash(nodeToken), address, t)
}

// Authenticate checks a node's id and token, for node routes outside this package.
func (s *Service) Authenticate(ctx context.Context, id, nodeToken string) error {
	return s.store.Authenticate(ctx, id, hash(nodeToken))
}

func (s *Service) AgentTarget(ctx context.Context, id string) (address, tokenHash string, err error) {
	return s.store.AgentTarget(ctx, id)
}

// Online says which nodes are online, from their heartbeats: no network call. Lists use it to
// skip nodes known to be down instead of waiting out an agent timeout for each.
func (s *Service) Online(ctx context.Context) (map[string]bool, error) {
	nodes, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	online := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		online[n.ID] = n.Status == "online"
	}
	return online, nil
}

func (s *Service) List(ctx context.Context) ([]Node, error) {
	nodes, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for i := range nodes {
		nodes[i].setStatus(now)
	}
	return nodes, nil
}

// SetRoles changes what a node is used for. Roles only decide where new workspaces, builds,
// apps and databases go: removing one leaves what already runs there alone.
func (s *Service) SetRoles(ctx context.Context, id string, roles []string) (Node, error) {
	roles, err := normalizeRoles(roles)
	if err != nil {
		return Node{}, err
	}
	if err := s.store.SetRoles(ctx, id, roles); err != nil {
		return Node{}, err
	}
	nodes, err := s.List(ctx)
	for _, n := range nodes {
		if n.ID == id {
			return n, err
		}
	}
	return Node{}, ErrNodeNotFound
}

func (s *Service) Remove(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// normalizeRoles dedupes and orders roles as in Roles; rejects unknown ones.
func normalizeRoles(in []string) ([]string, error) {
	out := []string{}
	for _, r := range Roles {
		if slices.Contains(in, r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 || len(out) != len(slices.Compact(slices.Sorted(slices.Values(in)))) {
		return nil, ErrInvalidRoles
	}
	return out, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b) // never returns an error (crypto/rand, Go 1.24+)
	return hex.EncodeToString(b)
}

func hash(token string) string { return agent.TokenHash(token) }
