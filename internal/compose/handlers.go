package compose

import (
	"net/http"

	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes must be mounted behind user auth. A compose file can hold secrets: only those who deploy
// apps read or sync it.
func (h *Handler) Routes(r *whttp.Router) {
	d := r.With(auth.Require(auth.PermApps))
	d.Get("/api/projects/{name}/compose", h.handleGet)
	d.Post("/api/projects/{name}/compose/plan", h.handlePlan)
	d.Put("/api/projects/{name}/compose", h.handleApply)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	src, err := h.svc.Source(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, src, err)
}

// handlePlan says what a sync with the body's source would do, changing nothing.
func (h *Handler) handlePlan(w http.ResponseWriter, r *http.Request) {
	var src Source
	if whttp.Decode(w, r, &src) {
		d, err := h.svc.Plan(r.Context(), r.PathValue("name"), src)
		whttp.Reply(w, http.StatusOK, d, err)
	}
}

// handleApply syncs the project with the body's source, saves it and deploys the services.
func (h *Handler) handleApply(w http.ResponseWriter, r *http.Request) {
	var src Source
	if whttp.Decode(w, r, &src) {
		d, err := h.svc.Apply(r.Context(), r.PathValue("name"), src)
		whttp.Reply(w, http.StatusOK, d, err)
	}
}
