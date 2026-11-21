package fleet

import (
	"context"
	"slices"
)

// Placement says where something may run: on a node with Role. The errors are the caller's, so
// the message names what it was placing (a workspace needs dev, an app or database prod).
type Placement struct {
	Role      string
	NoNode    error // no online node has the role
	WrongRole error // the chosen node doesn't have it
}

// Pick returns the node asked for (want is a name or an ID) if it has the role, or, with want
// empty, the online node with the role and the least load (nil load: the first one).
func (s *Service) Pick(ctx context.Context, p Placement, want string, load map[string]int) (Node, error) {
	nodes, err := s.List(ctx)
	if err != nil {
		return Node{}, err
	}
	if want != "" {
		return chosen(nodes, p, want)
	}
	return leastLoaded(nodes, p, load)
}

func chosen(nodes []Node, p Placement, want string) (Node, error) {
	for _, n := range nodes {
		if n.ID == want || n.Name == want {
			if !slices.Contains(n.Roles, p.Role) {
				return Node{}, p.WrongRole
			}
			return n, nil
		}
	}
	return Node{}, ErrNodeNotFound
}

func leastLoaded(nodes []Node, p Placement, load map[string]int) (Node, error) {
	best := -1
	for i, n := range nodes {
		if n.Status != "online" || !slices.Contains(n.Roles, p.Role) {
			continue
		}
		if best < 0 || load[n.ID] < load[nodes[best].ID] {
			best = i
		}
	}
	if best < 0 {
		return Node{}, p.NoNode
	}
	return nodes[best], nil
}
