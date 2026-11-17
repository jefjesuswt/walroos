// Package agent is the node's HTTP API for the hub (hub → node), plus the client the hub uses.
//
// A request is accepted only if both hold:
//   - its tailnet identity (WhoIs) is the hub's node, pinned by StableID. The agent learns that
//     ID from the heartbeat response, the same channel it already trusts for its node token.
//   - it carries the sha256 of this node's token, which is what the hub stores.
package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"github.com/jefjesuswt/walroos/internal/version"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"tailscale.com/client/local"
)

// Port is where node agents listen for the hub.
const Port = "7070"

// Info is what GET /v1/info returns.
type Info struct {
	Hostname string `json:"hostname"`
	Walroos  string `json:"walroos"`
	Podman   string `json:"podman"` // empty when podman is not installed
}

// Identify returns the tailnet StableNodeID of whoever sent the request.
type Identify func(*http.Request) (string, error)

// Tailscale identifies callers with the host tailscaled's WhoIs. In userspace mode
// (--tun=userspace-networking) tailnet traffic arrives on loopback and WhoIs still resolves it.
func Tailscale(lc *local.Client) Identify {
	return func(r *http.Request) (string, error) {
		who, err := lc.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			return "", err
		}
		return string(who.Node.StableID), nil
	}
}

type Server struct {
	ws        *Workspaces
	identify  Identify
	tokenHash []byte
	hubID     atomic.Pointer[string]
}

// NewServer serves the agent API for the node that owns nodeToken.
func NewServer(identify Identify, nodeToken string) *Server {
	return &Server{ws: NewWorkspaces(), identify: identify, tokenHash: []byte(TokenHash(nodeToken))}
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
	return r
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := s.hubID.Load()
		if hub == nil {
			whttp.ErrorJSON(w, http.StatusServiceUnavailable, "hub identity not known yet, waiting for the first heartbeat")
			return
		}
		if id, err := s.identify(r); err != nil || id != *hub {
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
	host, _ := os.Hostname()
	whttp.JSON(w, http.StatusOK, Info{Hostname: host, Walroos: version.Version, Podman: PodmanVersion(r.Context())})
}

// PodmanVersion returns the podman client version, or "" if podman isn't available.
func PodmanVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "podman", "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TokenHash is how node tokens are stored on the hub and presented to the agent.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
