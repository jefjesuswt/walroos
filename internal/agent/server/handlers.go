package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/agent/apps"
	"github.com/jefjesuswt/walroos/internal/agent/databases"
	"github.com/jefjesuswt/walroos/internal/agent/workspaces"
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
		RepoURL  string `json:"repo_url"`
		Branch   string `json:"branch"`
		GitToken string `json:"git_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := workspaces.Detect(r.Context(), body.RepoURL, body.Branch, body.GitToken)
	switch {
	case errors.Is(err, workspaces.ErrDetectClone):
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
		agent.WorkspaceSpec
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
	case errors.Is(err, workspaces.ErrInvalid):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, workspaces.ErrNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, workspaces.ErrExists), errors.Is(err, workspaces.ErrBusy):
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
	r.Post("/v1/apps/{name}/power", s.handleAppPower)
	r.Put("/v1/tunnel", s.handleSetTunnel)

	r.Get("/v1/databases", s.handleListDatabases)
	r.Post("/v1/databases", s.handleCreateDatabase)
	r.Delete("/v1/databases/{name}", s.handleDeleteDatabase)
	r.Post("/v1/databases/{name}/backups", s.handleBackupDatabase)
	r.Get("/v1/databases/{name}/backups", s.handleListBackups)
	r.Get("/v1/databases/{name}/backups/{file}", s.handleDownloadBackup)
	r.Post("/v1/databases/{name}/restore", s.handleRestoreDatabase)
}

func databaseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, databases.ErrInvalid):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, databases.ErrNotFound), errors.Is(err, databases.ErrNoBackup):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, apps.ErrBusy):
		whttp.ErrorJSON(w, http.StatusConflict, "a backup, restore or delete of this database is already running")
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	list, err := s.dbs.ListDatabases(r.Context())
	if err != nil {
		databaseError(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var spec agent.DatabaseSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := s.dbs.CreateDatabase(r.Context(), spec)
	if err != nil {
		databaseError(w, err)
		return
	}
	whttp.JSON(w, http.StatusCreated, d)
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if err := s.dbs.DeleteDatabase(r.Context(), r.PathValue("name"), r.URL.Query().Get("purge") == "1"); err != nil {
		databaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBackupDatabase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keep int `json:"keep"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	b, err := s.dbs.BackupDatabase(r.Context(), r.PathValue("name"), in.Keep)
	if err != nil {
		databaseError(w, err)
		return
	}
	whttp.JSON(w, http.StatusCreated, b)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.dbs.ListBackups(r.Context(), r.PathValue("name"))
	if err != nil {
		databaseError(w, err)
		return
	}
	whttp.JSON(w, http.StatusOK, list)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	f, err := s.dbs.OpenBackup(r.Context(), r.PathValue("name"), r.PathValue("file"))
	if err != nil {
		databaseError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	io.Copy(w, f)
}

func (s *Server) handleRestoreDatabase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.dbs.RestoreDatabase(r.Context(), r.PathValue("name"), in.File); err != nil {
		databaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetTunnel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	err := s.apps.SetTunnel(r.Context(), in.Token)
	switch {
	case errors.Is(err, apps.ErrInvalidTunnel):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case err != nil:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleStream streams a deploy or build as it happens. A build takes minutes; the hub stores
// the lines as they come, so the UI shows a live log.
func (s *Server) handleStream(op func(context.Context, agent.AppSpec, io.Writer) (agent.Release, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var spec agent.AppSpec
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
			lw.send(agent.DeployLine{Error: err.Error()})
			return
		}
		lw.send(agent.DeployLine{Release: &rel})
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
	if errors.Is(err, apps.ErrNotFound) {
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, logs)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	if err := s.apps.Delete(r.Context(), r.PathValue("name"), r.URL.Query().Get("purge") == "1"); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, apps.ErrBusy) {
			code = http.StatusConflict
		}
		whttp.ErrorJSON(w, code, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAppPower stops, starts or restarts the app's running release: {"action": "stop"}.
func (s *Server) handleAppPower(w http.ResponseWriter, r *http.Request) {
	var in struct{ Action string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		whttp.ErrorJSON(w, http.StatusBadRequest, "bad JSON")
		return
	}
	err := s.apps.Power(r.Context(), r.PathValue("name"), in.Action)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, apps.ErrPower):
		whttp.ErrorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, apps.ErrNotFound):
		whttp.ErrorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, apps.ErrBusy):
		whttp.ErrorJSON(w, http.StatusConflict, err.Error())
	default:
		whttp.ErrorJSON(w, http.StatusInternalServerError, err.Error())
	}
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
		l.sendLocked(agent.DeployLine{Log: string(l.buf[:i])})
		l.buf = l.buf[i+skip:]
	}
}

const maxLine = 64 << 10

// Flush sends a last unterminated line.
func (l *lineWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		l.sendLocked(agent.DeployLine{Log: string(l.buf)})
		l.buf = nil
	}
}

func (l *lineWriter) send(d agent.DeployLine) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sendLocked(d)
}

func (l *lineWriter) sendLocked(d agent.DeployLine) {
	l.enc.Encode(d)
	l.flush()
}
