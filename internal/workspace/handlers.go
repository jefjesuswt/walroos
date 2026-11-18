package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Routes must be mounted behind user auth.
func (h *Handler) Routes(r *whttp.Router) {
	r.Get("/api/workspaces", h.handleList)
	r.Post("/api/workspaces", h.handleCreate)
	r.Get("/api/workspaces/{name}", h.handleGet)
	r.Post("/api/workspaces/{name}/start", h.handleStart)
	r.Post("/api/workspaces/{name}/stop", h.handleStop)
	r.Post("/api/workspaces/{name}/rebuild", h.handleRebuild)
	r.Get("/api/workspaces/{name}/env", h.handleGetEnv)
	r.Put("/api/workspaces/{name}/env", h.handlePutEnv)
	r.Delete("/api/workspaces/{name}", h.handleDelete)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	if err != nil {
		errorJSON(w, err)
		return
	}
	if list == nil {
		list = []Workspace{}
	}
	whttp.JSON(w, http.StatusOK, list)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u, _ := auth.FromContext(r.Context())
	ws, err := h.svc.Create(r.Context(), u.Login, in)
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusCreated, ws)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, ws)
}

func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request) {
	h.op(w, r, h.svc.Start)
}

func (h *Handler) handleStop(w http.ResponseWriter, r *http.Request) {
	h.op(w, r, h.svc.Stop)
}

func (h *Handler) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	if err != nil {
		errorJSON(w, err)
		return
	}
	if vars == nil {
		vars = []EnvVar{}
	}
	whttp.JSON(w, http.StatusOK, vars)
}

func (h *Handler) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	var vars []EnvVar
	if err := json.NewDecoder(r.Body).Decode(&vars); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.svc.SetEnv(r.Context(), r.PathValue("name"), vars); err != nil {
		errorJSON(w, err)
		return
	}
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, vars)
}

func (h *Handler) handleRebuild(w http.ResponseWriter, r *http.Request) {
	ws, err := h.svc.Rebuild(r.Context(), r.PathValue("name"))
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusAccepted, ws)
}

func (h *Handler) op(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, name string) (Workspace, error)) {
	ws, err := fn(r.Context(), r.PathValue("name"))
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, ws)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("name")); err != nil {
		errorJSON(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func errorJSON(w http.ResponseWriter, err error) {
	var ae *agent.AgentError
	switch {
	case errors.Is(err, ErrInvalid):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, fleet.ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrExists), errors.Is(err, ErrNoNode), errors.Is(err, ErrNodeRole), errors.Is(err, ErrAgentBusy):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case errors.As(err, &ae), errors.Is(err, agent.ErrNoAddress):
		whttp.ErrorJSON(w, http.StatusBadGateway, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
