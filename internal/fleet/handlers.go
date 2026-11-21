package fleet

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

type Handler struct {
	svc    *Service
	hubURL string        // how nodes reach this hub, used in the join command
	hubID  string        // the hub's tailnet StableNodeID; agents only accept calls from it
	agents *agent.Client // calls node agents over the tailnet
}

func NewHandler(svc *Service, hubURL, hubID string, agents *agent.Client) *Handler {
	return &Handler{svc: svc, hubURL: hubURL, hubID: hubID, agents: agents}
}

// NodeRoutes are called by node agents; they authenticate with join/node tokens.
func (h *Handler) NodeRoutes(r *whttp.Router) {
	r.Post("/api/nodes/join", h.handleJoin)
	r.Post("/api/nodes/{id}/heartbeat", h.handleHeartbeat)
}

// UserRoutes must be mounted behind user auth.
func (h *Handler) UserRoutes(r *whttp.Router) {
	r.Get("/api/nodes", h.handleList) // everyone: creating a workspace picks a node
	o := r.With(auth.Require(auth.PermNodes))
	o.Delete("/api/nodes/{id}", h.handleRemove)
	o.Put("/api/nodes/{id}/roles", h.handleSetRoles)
	o.Get("/api/nodes/{id}/agent", h.handleAgentInfo)
	o.Post("/api/nodes/join-tokens", h.handleNewJoinToken)
}

func (h *Handler) handleNewJoinToken(w http.ResponseWriter, req *http.Request) {
	token, expiresAt, err := h.svc.CreateJoinToken(req.Context())
	if err != nil {
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	whttp.JSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expiresAt,
		"command":    "walroos node join --hub " + h.hubURL + " --token " + token,
	})
}

func (h *Handler) handleJoin(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Token string   `json:"token"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	node, nodeToken, err := h.svc.Join(req.Context(), body.Token, body.Name, body.Roles)
	switch {
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrInvalidRoles):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidJoinToken):
		whttp.ErrorJSON(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrNodeNameAlreadyExists):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusCreated, map[string]any{"node": node, "token": nodeToken})
	}
}

func (h *Handler) handleSetRoles(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	node, err := h.svc.SetRoles(req.Context(), req.PathValue("id"), body.Roles)
	switch {
	case errors.Is(err, ErrInvalidRoles):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusOK, node)
	}
}

func (h *Handler) handleHeartbeat(w http.ResponseWriter, req *http.Request) {
	token, ok := bearerToken(req)
	if !ok {
		whttp.ErrorJSON(w, http.StatusUnauthorized, "missing bearer token")
		return
	}

	var t Telemetry
	if err := json.NewDecoder(req.Body).Decode(&t); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	address, _, _ := net.SplitHostPort(req.RemoteAddr)
	err := h.svc.Heartbeat(req.Context(), req.PathValue("id"), token, address, t)
	switch {
	case errors.Is(err, ErrUnauthorized):
		whttp.ErrorJSON(w, http.StatusUnauthorized, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusOK, map[string]string{"hub_id": h.hubID})
	}
}

// handleAgentInfo calls the node's agent: proves the hub → node path works.
func (h *Handler) handleAgentInfo(w http.ResponseWriter, req *http.Request) {
	address, tokenHash, err := h.svc.AgentTarget(req.Context(), req.PathValue("id"))
	switch {
	case errors.Is(err, ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	info, err := h.agents.Info(req.Context(), address, tokenHash)
	switch {
	case errors.Is(err, agent.ErrNoAddress):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusBadGateway, err.Error())
	default:
		whttp.JSON(w, http.StatusOK, info)
	}
}

func (h *Handler) handleList(w http.ResponseWriter, req *http.Request) {
	nodes, err := h.svc.List(req.Context())
	if err != nil {
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	whttp.JSON(w, http.StatusOK, nodes)
}

func (h *Handler) handleRemove(w http.ResponseWriter, req *http.Request) {
	err := h.svc.Remove(req.Context(), req.PathValue("id"))
	switch {
	case errors.Is(err, ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrNodeHasWorkspaces):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func bearerToken(req *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	return token, ok && token != ""
}
