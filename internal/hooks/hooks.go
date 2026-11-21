// Package hooks is how a forge deploys apps by itself, from outside the tailnet:
//   - POST /hooks/{github,gitea,gitlab}/{app}: a push webhook, signed with the app's webhook
//     secret. A push to the app's branch deploys that commit (apps with trigger on_push).
//   - POST /hooks/deploy/{app}: CI calls it with the app's deploy token once the tests pass
//     (trigger on_ci); the body may name the commit, {"sha": "..."}.
//
// The hub serves these on their own listener, published with Tailscale Funnel; nothing else is.
// Every refusal looks the same, so the public side can't tell which apps exist.
package hooks

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

const maxBody = 1 << 20 // a push payload with many commits stays well under this

var errDenied = whttp.Unauthorized("hook refused: unknown app, wrong secret or token, or the app doesn't deploy this way")

type Handler struct {
	apps *app.Service

	mu     sync.RWMutex
	status Status
}

// Status is where the hooks are reachable from the internet, for the UI.
type Status struct {
	URL   string `json:"url"`             // e.g. https://walroos-hub.tailnet.ts.net; "" if not published
	Error string `json:"error,omitempty"` // why they aren't (Funnel off in the tailnet policy, --dev)
}

func NewHandler(apps *app.Service) *Handler { return &Handler{apps: apps} }

// SetStatus records where the hooks listener was published, or why it wasn't.
func (h *Handler) SetStatus(st Status) {
	h.mu.Lock()
	h.status = st
	h.mu.Unlock()
}

// HookRoutes are the public ones: they authenticate with the app's secret or token.
func (h *Handler) HookRoutes(r *whttp.Router) {
	for _, f := range forges {
		r.Post("/hooks/"+f.name+"/{app}", h.handlePush(f))
	}
	r.Post("/hooks/deploy/{app}", h.handleDeploy)
}

// UserRoutes: who manages apps sees where to point the forge.
func (h *Handler) UserRoutes(r *whttp.Router) {
	r.With(auth.Require(auth.PermApps)).Get("/api/hooks", func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		st := h.status
		h.mu.RUnlock()
		whttp.JSON(w, http.StatusOK, st)
	})
}

// handlePush deploys the pushed commit if the push is signed with the app's secret and is to
// its branch. Other events (ping, a tag, another branch) answer 200 with what was ignored, so
// the forge's delivery log stays green.
func (h *Handler) handlePush(f forge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			whttp.ErrorJSON(w, http.StatusBadRequest, "can't read the body")
			return
		}
		t, err := h.apps.HookTarget(r.Context(), r.PathValue("app"))
		if err != nil || t.Trigger != app.OnPush || t.Secret == "" || !f.verify(r.Header, body, t.Secret) {
			whttp.WriteError(w, errDenied)
			return
		}
		ev, err := f.parse(r.Header, body)
		if err != nil {
			whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		if why := ev.skip(t); why != "" {
			whttp.JSON(w, http.StatusOK, map[string]any{"deployed": false, "reason": why})
			return
		}
		h.deploy(w, r, t, ev.sha)
	}
}

// handleDeploy is CI's call: Bearer <deploy token>, optional {"sha": "..."}.
func (h *Handler) handleDeploy(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	t, err := h.apps.HookTarget(r.Context(), r.PathValue("app"))
	if err != nil || token == "" || t.TokenHash == "" ||
		subtle.ConstantTimeCompare([]byte(agent.TokenHash(token)), []byte(t.TokenHash)) != 1 {
		whttp.WriteError(w, errDenied)
		return
	}
	var in struct {
		Sha string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		whttp.ErrorJSON(w, http.StatusBadRequest, "body must be JSON like {\"sha\": \"<commit>\"}, or empty")
		return
	}
	h.deploy(w, r, t, in.Sha)
}

// deploy queues it. An image app pulls its tag again: a commit means nothing to it.
func (h *Handler) deploy(w http.ResponseWriter, r *http.Request, t app.HookTarget, sha string) {
	if t.Image {
		sha = ""
	}
	d, err := h.apps.Deploy(r.Context(), t.App, strings.ToLower(sha))
	whttp.Reply(w, http.StatusAccepted, map[string]any{"deployed": true, "deployment": d.ID, "sha": d.Sha}, err)
}
