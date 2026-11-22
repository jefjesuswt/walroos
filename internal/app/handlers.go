package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/auth"
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
	// Everyone reads apps, deployments and logs; env holds secrets.
	r.Get("/api/apps", h.handleList)
	r.Get("/api/templates", func(w http.ResponseWriter, r *http.Request) { whttp.JSON(w, http.StatusOK, Templates) })
	r.Get("/api/deployments", h.handleAllDeployments)
	r.Get("/api/apps/{name}", h.handleGet)
	r.Get("/api/apps/{name}/logs", h.handleLogs)
	r.Get("/api/apps/{name}/deployments", h.handleDeployments)
	r.Get("/api/apps/{name}/deployments/{id}", h.handleDeployment)

	d := r.With(auth.Require(auth.PermApps))
	d.Post("/api/apps", h.handleCreate)
	d.Delete("/api/apps/{name}", h.handleDelete)
	d.Get("/api/apps/{name}/env", h.handleGetEnv)
	d.Put("/api/apps/{name}/env", h.handlePutEnv)
	d.Post("/api/apps/{name}/deploy", h.handleDeploy)
	d.Post("/api/apps/{name}/power", h.handlePower)
	d.Post("/api/apps/{name}/deployments/{id}/rollback", h.handleRollback)
	d.Put("/api/apps/{name}/domains", h.handlePutDomains)
	d.Put("/api/apps/{name}/volumes", h.handlePutVolumes)
	d.Put("/api/apps/{name}/source", h.handlePutSource)
	d.Post("/api/apps/from-template", h.handleFromTemplate)
	d.Put("/api/apps/{name}/trigger", h.handlePutTrigger)
	d.Get("/api/apps/{name}/webhook-secret", h.handleWebhookSecret)
	d.Post("/api/apps/{name}/webhook-secret", h.handleRotateWebhookSecret)
	d.Post("/api/apps/{name}/deploy-token", h.handleNewDeployToken)
	d.Delete("/api/apps/{name}/deploy-token", h.handleRevokeDeployToken)

}

func (h *Handler) handlePutDomains(w http.ResponseWriter, r *http.Request) {
	var domains []string
	if decode(w, r, &domains) {
		a, err := h.svc.SetDomains(r.Context(), r.PathValue("name"), domains)
		whttp.Reply(w, http.StatusOK, a, err)
	}
}

func (h *Handler) handleFromTemplate(w http.ResponseWriter, r *http.Request) {
	var in FromTemplateInput
	if decode(w, r, &in) {
		a, err := h.svc.CreateFromTemplate(r.Context(), in)
		whttp.Reply(w, http.StatusCreated, a, err)
	}
}

func (h *Handler) handlePutSource(w http.ResponseWriter, r *http.Request) {
	var in SourceInput
	if decode(w, r, &in) {
		a, err := h.svc.SetSource(r.Context(), r.PathValue("name"), in)
		whttp.Reply(w, http.StatusOK, a, err)
	}
}

func (h *Handler) handlePutVolumes(w http.ResponseWriter, r *http.Request) {
	var vols []agent.Volume
	if decode(w, r, &vols) {
		a, err := h.svc.SetVolumes(r.Context(), r.PathValue("name"), vols)
		whttp.Reply(w, http.StatusOK, a, err)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.List(r.Context())
	whttp.Reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if decode(w, r, &in) {
		a, err := h.svc.Create(r.Context(), in)
		whttp.Reply(w, http.StatusCreated, a, err)
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Get(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, a, err)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), r.PathValue("name"), r.URL.Query().Get("purge") == "1"); err != nil {
		whttp.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, vars, err)
}

func (h *Handler) handlePutEnv(w http.ResponseWriter, r *http.Request) {
	var vars []workspace.EnvVar
	if !decode(w, r, &vars) {
		return
	}
	if err := h.svc.SetEnv(r.Context(), r.PathValue("name"), vars); err != nil {
		whttp.WriteError(w, err)
		return
	}
	vars, err := h.svc.Env(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, vars, err)
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	logs, err := h.svc.Logs(r.Context(), r.PathValue("name"), lines)
	if err != nil {
		whttp.WriteError(w, err)
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
		whttp.Reply(w, http.StatusAccepted, d, err)
	}
}

// handlePower: {"action": "stop" | "start" | "restart"}; answers the app as it is now.
func (h *Handler) handlePower(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if decode(w, r, &in) {
		a, err := h.svc.Power(r.Context(), r.PathValue("name"), in.Action)
		whttp.Reply(w, http.StatusOK, a, err)
	}
}

func (h *Handler) handleAllDeployments(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.AllDeployments(r.Context())
	whttp.Reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleDeployments(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Deployments(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, list, err)
}

func (h *Handler) handleDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Deployment(r.Context(), r.PathValue("name"), r.PathValue("id"))
	whttp.Reply(w, http.StatusOK, d, err)
}

func (h *Handler) handleRollback(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Rollback(r.Context(), r.PathValue("name"), r.PathValue("id"))
	whttp.Reply(w, http.StatusAccepted, d, err)
}

func (h *Handler) handlePutTrigger(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Trigger string `json:"trigger"`
	}
	if decode(w, r, &in) {
		a, err := h.svc.SetTrigger(r.Context(), r.PathValue("name"), in.Trigger)
		whttp.Reply(w, http.StatusOK, a, err)
	}
}

func (h *Handler) handleWebhookSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.svc.WebhookSecret(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, map[string]string{"secret": secret}, err)
}

func (h *Handler) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.svc.RotateWebhookSecret(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusOK, map[string]string{"secret": secret}, err)
}

func (h *Handler) handleNewDeployToken(w http.ResponseWriter, r *http.Request) {
	token, err := h.svc.NewDeployToken(r.Context(), r.PathValue("name"))
	whttp.Reply(w, http.StatusCreated, map[string]string{"token": token}, err)
}

func (h *Handler) handleRevokeDeployToken(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RevokeDeployToken(r.Context(), r.PathValue("name")); err != nil {
		whttp.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
