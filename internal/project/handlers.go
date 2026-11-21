package project

import (
	"encoding/json"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes must be mounted behind user auth. Everyone sees projects; making and removing them is
// for roles that manage apps.
func (h *Handler) Routes(r *whttp.Router) {
	r.Get("/api/projects", h.handleList)
	r.Get("/api/projects/{name}", h.handleGet)
	d := r.With(auth.Require(auth.PermApps))
	d.Post("/api/projects", h.handleCreate)
	d.Delete("/api/projects/{name}", h.handleDelete)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	whttp.Reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Get(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, p, err)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	p, err := h.svc.Create(r.Context(), in)
	whttp.Reply(w, http.StatusCreated, p, err)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("name")); err != nil {
		whttp.Reply(w, 0, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
