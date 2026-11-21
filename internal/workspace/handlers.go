package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/github"
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
	r.Post("/api/workspaces/detect", h.handleDetect)
	r.Get("/api/workspaces/{name}", h.handleGet)
	// Acting on one: its owner, or a role that may touch anyone's (the hub's owner).
	own := r.With(h.ownsWorkspace)
	own.Post("/api/workspaces/{name}/start", h.handleStart)
	own.Post("/api/workspaces/{name}/stop", h.handleStop)
	own.Post("/api/workspaces/{name}/rebuild", h.handleRebuild)
	own.Get("/api/workspaces/{name}/env", h.handleGetEnv)
	own.Put("/api/workspaces/{name}/env", h.handlePutEnv)
	own.Delete("/api/workspaces/{name}", h.handleDelete)

	r.Get("/api/projects/stacks", h.handleStacks)
	r.Post("/api/projects", h.handleCreateProject)
	r.Get("/api/settings/github", h.handleGetGitHub) // who repos are created as; never the token
	s := r.With(auth.Require(auth.PermSettings))
	s.Put("/api/settings/github", h.handlePutGitHub)
	s.Delete("/api/settings/github", h.handleDeleteGitHub)
}

// ownsWorkspace answers 403 unless the caller may act on the workspace in the path.
func (h *Handler) ownsWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h.svc.Authorize(r.Context(), auth.Caller(r.Context()), r.PathValue("name")); err != nil {
			errorJSON(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) handleStacks(w http.ResponseWriter, r *http.Request) {
	whttp.JSON(w, http.StatusOK, agent.ProjectStacks)
}

func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var in ProjectInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u := auth.Caller(r.Context())
	if in.GitHub { // the repo goes on the owner's account, with their token
		if err := u.Check(auth.PermSettings); err != nil {
			errorJSON(w, err)
			return
		}
	}
	ws, err := h.svc.CreateProject(r.Context(), u.Login, in)
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusCreated, ws)
}

func (h *Handler) handleGetGitHub(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.GitHub(r.Context())
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, st)
}

func (h *Handler) handlePutGitHub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	st, err := h.svc.SetGitHub(r.Context(), in.Token)
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, st)
}

func (h *Handler) handleDeleteGitHub(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteGitHub(r.Context()); err != nil {
		errorJSON(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (h *Handler) handleDetect(w http.ResponseWriter, r *http.Request) {
	var in DetectInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := h.svc.Detect(r.Context(), in)
	if err != nil {
		errorJSON(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(d)
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
	case errors.Is(err, auth.ErrForbidden):
		whttp.ErrorJSON(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRepoURL), errors.Is(err, ErrStack), errors.Is(err, ErrStackVersion),
		errors.Is(err, github.ErrToken), errors.Is(err, github.ErrScope):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrRepo):
		whttp.ErrorJSON(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, fleet.ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrExists), errors.Is(err, ErrNoNode), errors.Is(err, ErrNodeRole), errors.Is(err, ErrAgentBusy),
		errors.Is(err, ErrNoGitHub), errors.Is(err, github.ErrRepoExists):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case errors.As(err, &ae), errors.Is(err, agent.ErrNoAddress), errors.Is(err, github.ErrUnavailable):
		whttp.ErrorJSON(w, http.StatusBadGateway, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
