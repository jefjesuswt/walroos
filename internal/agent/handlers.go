package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"

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
	r.Post("/v1/detect", s.handleDetect)
}

// handleDetect proposes a devcontainer for a repo: shallow clone, scan, answer. Nothing is kept.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RepoURL string `json:"repo_url"`
		Branch  string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := Detect(r.Context(), body.RepoURL, body.Branch)
	switch {
	case errors.Is(err, ErrDetectClone):
		whttp.ErrorJSON(w, http.StatusUnprocessableEntity, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		whttp.JSON(w, http.StatusOK, d)
	}
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
	var body struct {
		WorkspaceSpec
		Env []string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ws, err := s.ws.Create(r.Context(), body.WorkspaceSpec, body.Env)
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
	var body struct {
		Env []string `json:"env"`
	}
	json.NewDecoder(r.Body).Decode(&body) // empty body is fine: no env
	ws, err := s.ws.Rebuild(r.Context(), r.PathValue("name"), body.Env)
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

func (s *Server) appRoutes(r *whttp.Router) {
	r.Get("/v1/apps", s.handleListApps)
	r.Post("/v1/apps/{name}/deploy", s.handleStream(s.apps.Deploy))
	r.Post("/v1/apps/{name}/build", s.handleStream(s.apps.Build))
	r.Get("/v1/apps/{name}/logs", s.handleAppLogs)
	r.Delete("/v1/apps/{name}", s.handleDeleteApp)
}

// DeployLine is one line of a deploy's NDJSON answer: log output while it runs, then one
// line with Release or Error.
type DeployLine struct {
	Log     string   `json:"log,omitempty"`
	Release *Release `json:"release,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// handleStream streams a deploy or build as it happens. A build takes minutes; the hub stores
// the lines as they come, so the UI shows a live log.
func (s *Server) handleStream(op func(context.Context, AppSpec, io.Writer) (Release, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var spec AppSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		spec.Name = r.PathValue("name")
		w.Header().Set("Content-Type", "application/x-ndjson")
		lw := &lineWriter{enc: json.NewEncoder(w), flush: http.NewResponseController(w).Flush}
		rel, err := op(r.Context(), spec, lw)
		lw.Flush()
		if err != nil {
			lw.send(DeployLine{Error: err.Error()})
			return
		}
		lw.send(DeployLine{Release: &rel})
	}
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.apps.List(r.Context())
	if err != nil {
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	whttp.JSON(w, http.StatusOK, list)
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	logs, err := s.apps.Logs(r.Context(), r.PathValue("name"), min(cmpInt(lines, 200), 5000))
	if errors.Is(err, ErrAppNotFound) {
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, logs)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	if err := s.apps.Delete(r.Context(), r.PathValue("name")); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrAppBusy) {
			code = http.StatusConflict
		}
		whttp.ErrorJSON(w, code, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cmpInt(n, fallback int) int {
	if n <= 0 {
		return fallback
	}
	return n
}

// lineWriter turns build output into DeployLines, one per line, flushed as they come.
type lineWriter struct {
	mu    sync.Mutex
	buf   []byte
	enc   *json.Encoder
	flush func() error
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i, skip := bytes.IndexByte(l.buf, '\n'), 1
		if i < 0 || i > maxLine { // a line with no end in sight is cut, not buffered whole
			if len(l.buf) < maxLine {
				return len(p), nil
			}
			i, skip = maxLine, 0
		}
		l.sendLocked(DeployLine{Log: string(l.buf[:i])})
		l.buf = l.buf[i+skip:]
	}
}

const maxLine = 64 << 10

// Flush sends a last unterminated line.
func (l *lineWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		l.sendLocked(DeployLine{Log: string(l.buf)})
		l.buf = nil
	}
}

func (l *lineWriter) send(d DeployLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sendLocked(d)
}

func (l *lineWriter) sendLocked(d DeployLine) {
	l.enc.Encode(d)
	l.flush()
}
