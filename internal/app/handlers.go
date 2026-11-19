package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jefjesuswt/walroos/internal/agent"
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
	r.Get("/api/apps", h.handleList)
	r.Get("/api/deployments", h.handleAllDeployments)
	r.Post("/api/apps", h.handleCreate)
	r.Get("/api/apps/{name}", h.handleGet)
	r.Delete("/api/apps/{name}", h.handleDelete)
	r.Get("/api/apps/{name}/env", h.handleGetEnv)
	r.Put("/api/apps/{name}/env", h.handlePutEnv)
	r.Get("/api/apps/{name}/logs", h.handleLogs)
	r.Post("/api/apps/{name}/deploy", h.handleDeploy)
	r.Get("/api/apps/{name}/deployments", h.handleDeployments)
	r.Get("/api/apps/{name}/deployments/{id}", h.handleDeployment)
	r.Post("/api/apps/{name}/deployments/{id}/rollback", h.handleRollback)
	r.Put("/api/apps/{name}/domains", h.handlePutDomains)
	r.Get("/api/settings/cloudflare", h.handleGetCloudflare)
	r.Put("/api/settings/cloudflare", h.handlePutCloudflare)
}

func (h *Handler) handlePutDomains(w http.ResponseWriter, r *http.Request) {
	var domains []string
	if decode(w, r, &domains) {
		a, err := h.svc.SetDomains(r.Context(), r.PathValue("name"), domains)
		reply(w, http.StatusOK, a, err)
	}
}

func (h *Handler) handleGetCloudflare(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Cloudflare(r.Context())
	reply(w, http.StatusOK, st, err)
}

func (h *Handler) handlePutCloudflare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if decode(w, r, &in) {
		st, err := h.svc.SetCloudflare(r.Context(), in.Token)
		reply(w, http.StatusOK, st, err)
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

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if decode(w, r, &in) {
		a, err := h.svc.Create(r.Context(), in)
		reply(w, http.StatusCreated, a, err)
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Get(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, a, err)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("name")); err != nil {
		errorJSON(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, vars, err)
}

func (h *Handler) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	var vars []workspace.EnvVar
	if !decode(w, r, &vars) {
		return
	}
	if err := h.svc.SetEnv(r.Context(), r.PathValue("name"), vars); err != nil {
		errorJSON(w, err)
		return
	}
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, vars, err)
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	logs, err := h.svc.Logs(r.Context(), r.PathValue("name"), lines)
	if err != nil {
		errorJSON(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, logs)
}

func (h *Handler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sha string `json:"sha"`
	}
	if decode(w, r, &in) {
		d, err := h.svc.Deploy(r.Context(), r.PathValue("name"), in.Sha)
		reply(w, http.StatusAccepted, d, err)
	}
}

func (h *Handler) handleAllDeployments(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.AllDeployments(r.Context())
	reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleDeployments(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Deployments(r.Context(), r.PathValue("name"))
	reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Deployment(r.Context(), r.PathValue("name"), r.PathValue("id"))
	reply(w, http.StatusOK, d, err)
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Rollback(r.Context(), r.PathValue("name"), r.PathValue("id"))
	reply(w, http.StatusAccepted, d, err)
}

func errorJSON(w http.ResponseWriter, err error) {
	var ae *agent.AgentError
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, workspace.ErrInvalid), errors.Is(err, ErrDomain), errors.Is(err, ErrNoZone):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrDeployNotFound), errors.Is(err, fleet.ErrNodeNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrExists), errors.Is(err, ErrNoNode), errors.Is(err, ErrNodeRole), errors.Is(err, ErrNoImage), errors.Is(err, ErrBusy),
		errors.Is(err, ErrNoCloudflare), errors.Is(err, ErrDomainTaken):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	case errors.As(err, &ae), errors.Is(err, agent.ErrNoAddress):
		whttp.ErrorJSON(w, http.StatusBadGateway, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}
