package database

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/workspace"
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
	r = r.With(auth.Require(auth.PermDatabases)) // passwords and data: deployers and the owner
	r.Get("/api/databases", h.handleList)
	r.Post("/api/databases", h.handleCreate)
	r.Get("/api/databases/engines", h.handleEngines)
	r.Get("/api/databases/{name}", h.handleGet)
	r.Delete("/api/databases/{name}", h.handleDelete)
	r.Put("/api/databases/{name}/schedule", h.handleSchedule)
	r.Post("/api/databases/{name}/link", h.handleLink)
	r.Get("/api/databases/{name}/backups", h.handleBackups)
	r.Post("/api/databases/{name}/backups", h.handleBackup)
	r.Get("/api/databases/{name}/backups/{file}", h.handleDownload)
	r.Post("/api/databases/{name}/restore", h.handleRestore)
}

func (h *Handler) handleEngines(w http.ResponseWriter, r *http.Request) {
	whttp.JSON(w, http.StatusOK, Engines())
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if decode(w, r, &in) {
		d, err := h.svc.Create(r.Context(), in)
		reply(w, http.StatusCreated, d, err)
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Get(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, d, err)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("name"), r.URL.Query().Get("purge") == "1"); err != nil {
		errorJSON(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSchedule(w http.ResponseWriter, r *http.Request) {
	var sc Schedule
	if decode(w, r, &sc) {
		d, err := h.svc.SetSchedule(r.Context(), r.PathValue("name"), sc)
		reply(w, http.StatusOK, d, err)
	}
}

func (h *Handler) handleLink(w http.ResponseWriter, r *http.Request) {
	var in LinkInput
	if decode(w, r, &in) {
		vars, err := h.svc.Link(r.Context(), r.PathValue("name"), in)
		if vars == nil {
			vars = []workspace.EnvVar{}
		}
		reply(w, http.StatusOK, vars, err)
	}
}

func (h *Handler) handleBackups(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Backups(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleBackup(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Backup(r.Context(), r.PathValue("name"))
	reply(w, http.StatusCreated, b, err)
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	body, size, err := h.svc.Download(r.Context(), r.PathValue("name"), file)
	if err != nil {
		errorJSON(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`) // file matched fileRe: no quotes
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	io.Copy(w, body)
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File string `json:"file"`
	}
	if decode(w, r, &in) {
		if err := h.svc.Restore(r.Context(), r.PathValue("name"), in.File); err != nil {
			errorJSON(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func reply[T any](w http.ResponseWriter, code int, v T, err error) {
	if err != nil {
		errorJSON(w, err)
		return
	}
	whttp.JSON(w, code, v)
}

func errorJSON(w http.ResponseWriter, err error) {
	var ae *agent.AgentError
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrSchedule), errors.Is(err, ErrEnvKey), errors.Is(err, ErrInvalidFile), errors.Is(err, ErrOtherNode), errors.Is(err, ErrOtherProj):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoBackup), errors.Is(err, app.ErrNotFound), errors.Is(err, fleet.ErrNodeNotFound), errors.Is(err, app.ErrNoProject):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrExists), errors.Is(err, ErrNoNode), errors.Is(err, ErrNodeRole), errors.Is(err, ErrBusy):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case errors.As(err, &ae), errors.Is(err, agent.ErrNoAddress):
		whttp.ErrorJSON(w, http.StatusBadGateway, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
