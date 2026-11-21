package settings

import (
	"encoding/json"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes: anyone sees whose GitHub repos are created as; who deploys apps sees Cloudflare's
// zones (the domains dialog); only settings may change a token.
func (h *Handler) Routes(r *whttp.Router) {
	r.Get("/api/settings/github", h.handleGetGitHub)
	r.With(auth.Require(auth.PermApps)).Get("/api/settings/cloudflare", h.handleGetCloudflare)
	s := r.With(auth.Require(auth.PermSettings))
	s.Put("/api/settings/github", h.handlePutGitHub)
	s.Delete("/api/settings/github", h.handleDeleteGitHub)
	s.Put("/api/settings/cloudflare", h.handlePutCloudflare)
}

func (h *Handler) handleGetGitHub(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.GitHub(r.Context())
	whttp.Reply(w, http.StatusOK, st, err)
}

func (h *Handler) handleGetCloudflare(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Cloudflare(r.Context())
	whttp.Reply(w, http.StatusOK, st, err)
}

func (h *Handler) handlePutGitHub(w http.ResponseWriter, r *http.Request) {
	if token, ok := readToken(w, r); ok {
		st, err := h.svc.SetGitHub(r.Context(), token)
		whttp.Reply(w, http.StatusOK, st, err)
	}
}

func (h *Handler) handlePutCloudflare(w http.ResponseWriter, r *http.Request) {
	if token, ok := readToken(w, r); ok {
		st, err := h.svc.SetCloudflare(r.Context(), token)
		whttp.Reply(w, http.StatusOK, st, err)
	}
}

func (h *Handler) handleDeleteGitHub(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteGitHub(r.Context()); err != nil {
		whttp.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return "", false
	}
	return in.Token, true
}
