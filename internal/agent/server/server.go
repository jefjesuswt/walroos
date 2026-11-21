// Package server is the agent's HTTP API for the hub (hub → node).
//
// A request is accepted only if both hold:
//   - its tailnet identity (WhoIs) is the hub's node, pinned by StableID. The agent learns that
//     ID from the heartbeat response, the same channel it already trusts for its node token.
//   - it carries the sha256 of this node's token, which is what the hub stores.
package server

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/apps"
	"github.com/jefjesuswt/walroos/internal/agent/databases"
	"github.com/jefjesuswt/walroos/internal/agent/host"
	"github.com/jefjesuswt/walroos/internal/agent/quadlet"
	"github.com/jefjesuswt/walroos/internal/agent/sshgw"
	"github.com/jefjesuswt/walroos/internal/agent/workspaces"
	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"golang.org/x/crypto/ssh"
)

// Server is the agent's HTTP API on the tailnet (:7070): only the hub may call it, with the
// node token's hash. It composes what the node runs: workspaces, app releases, databases.
type Server struct {
	ws        *workspaces.Workspaces
	apps      *apps.Apps
	dbs       *databases.Databases
	identify  agent.Identify
	tokenHash []byte
	hubID     atomic.Pointer[string]
}

// New serves the agent API for the node that owns nodeToken.
func New(identify agent.Identify, nodeToken string) *Server {
	q := quadlet.New() // apps and databases share the unit directory and its locks
	return &Server{ws: workspaces.New(), apps: apps.New(q), dbs: databases.New(q), identify: identify, tokenHash: []byte(agent.TokenHash(nodeToken))}
}

// SetRelay sets the command that runs Relay (`walroos node relay`); app health checks use it.
func (s *Server) SetRelay(relay []string) { s.apps.SetRelay(relay) }

// SetDial sets how to reach other nodes (tsnet's Dial); a prod node pulls builds with it.
func (s *Server) SetDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	s.apps.SetDial(dial)
}

// SetHubID pins the hub's StableNodeID. Until it's set every request gets 503.
func (s *Server) SetHubID(id string) {
	if id != "" {
		s.hubID.Store(&id)
	}
}

func (s *Server) Handler() http.Handler {
	r := whttp.NewRouter()
	r.Use(s.auth)
	r.Get("/v1/info", handleInfo)
	s.workspaceRoutes(r)
	s.appRoutes(r)
	return r
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := s.hubID.Load()
		if hub == nil {
			whttp.ErrorJSON(w, http.StatusServiceUnavailable, "hub identity not known yet, waiting for the first heartbeat")
			return
		}
		if id, err := s.identify(r.Context(), r.RemoteAddr); err != nil || id != *hub {
			whttp.ErrorJSON(w, http.StatusForbidden, "only the hub can call this agent")
			return
		}
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), s.tokenHash) != 1 {
			whttp.ErrorJSON(w, http.StatusUnauthorized, "invalid node token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	name, _ := os.Hostname()
	whttp.JSON(w, http.StatusOK, agent.Info{Hostname: name, Walroos: version.Version, Podman: host.PodmanVersion(r.Context())})
}

// RegistryHandler serves this node's build registry to other nodes (see apps.RegistryHandler).
func (s *Server) RegistryHandler() http.Handler { return s.apps.RegistryHandler(string(s.tokenHash)) }

// SSHGateway is the SSH gateway into this node's workspaces (see sshgw.New).
func (s *Server) SSHGateway(authorize sshgw.SSHAuthorize, hostKey ssh.Signer, relay []string) *sshgw.SSHGateway {
	return sshgw.New(s.identify, authorize, hostKey, relay, s.ws)
}
