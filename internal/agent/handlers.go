package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jefjesuswt/walroos/shared/whttp"
)

func (s *Server) workspaceRoutes(r *whttp.Router) {
	r.Get("/v1/workspaces", s.handleListWorkspaces)
	r.Post("/v1/workspaces", s.handleCreateWorkspace)
	r.Get("/v1/workspaces/{name}", s.handleGetWorkspace)
	r.Post("/v1/workspaces/{name}/start", s.handleStartWorkspace)
	r.Post("/v1/workspaces/{name}/stop", s.handleStopWorkspace)
	r.Post("/v1/workspaces/{name}/rebuild", s.handleRebuildWorkspace)
	r.Delete("/v1/workspaces/{name}", s.handleDeleteWorkspace)
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	list, err := s.ws.List(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, list)
}

// handleCreateWorkspace answers 202 right away; creation (clone, build, run) continues in the background.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var spec WorkspaceSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ws, err := s.ws.Create(r.Context(), spec)
	if err != nil {
		workspaceError(w, err)
		return
	}
	whttp.JSON(w, http.StatusAccepted, ws)
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.ws.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		workspaceError(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, ws)
}

func (s *Server) handleStartWorkspace(w http.ResponseWriter, r *http.Request) {
	s.workspaceOp(w, r, s.ws.Start)
}

func (s *Server) handleStopWorkspace(w http.ResponseWriter, r *http.Request) {
	s.workspaceOp(w, r, s.ws.Stop)
}

func (s *Server) handleRebuildWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.ws.Rebuild(r.Context(), r.PathValue("name"))
	if err != nil {
		workspaceError(w, err)
		return
	}
	whttp.JSON(w, http.StatusAccepted, ws)
}

func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.ws.Delete(r.Context(), r.PathValue("name")); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workspaceOp runs op and answers with the workspace's new state.
func (s *Server) workspaceOp(w http.ResponseWriter, r *http.Request, op func(ctx context.Context, name string) error) {
	name := r.PathValue("name")
	if err := op(r.Context(), name); err != nil {
		workspaceError(w, err)
		return
	}
	s.handleGetWorkspace(w, r)
}

func workspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidWorkspace):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrWorkspaceNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrWorkspaceExists), errors.Is(err, ErrWorkspaceBusy):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
