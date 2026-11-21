package hub

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jefjesuswt/walroos/internal/hooks"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/database"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/mcp"
	"github.com/jefjesuswt/walroos/internal/project"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/settings"
	"github.com/jefjesuswt/walroos/internal/sshkeys"
	"github.com/jefjesuswt/walroos/internal/web"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/whttp"
)

// services are the hub's domains, wired to each other.
type services struct {
	fleet     *fleet.Service
	fleetH    *fleet.Handler
	keys      *sshkeys.Handler
	settings  *settings.Service
	workspace *workspace.Service
	app       *app.Service
	database  *database.Service
	project   *project.Service
	hooks     *hooks.Handler
}

// newServices builds the domains and starts the background workers (deploys, backups).
func newServices(ctx context.Context, db *config.Database, box *secretbox.Box, nw network) services {
	s := services{fleet: fleet.NewService(fleet.NewStore(db)), settings: settings.NewService(db, box)}
	s.fleetH = fleet.NewHandler(s.fleet, nw.url, nw.id, nw.agents)
	s.keys = sshkeys.NewHandler(sshkeys.NewStore(db), s.fleet.Authenticate)
	s.workspace = workspace.NewService(workspace.NewStore(db), s.fleet, nw.agents, box, s.settings)
	s.app = app.NewService(app.NewStore(db), s.fleet, nw.agents, box, s.settings)
	s.database = database.NewService(database.NewStore(db), s.fleet, nw.agents, box, s.app)
	s.project = project.NewService(db, s.app, s.database, box)
	s.app.SetResolver(s.project.Vars) // ${{service.KEY}} in app variables
	s.hooks = hooks.NewHandler(s.app)
	go s.app.Run(ctx)
	go s.database.Run(ctx)
	return s
}

// routes: node-facing routes authenticate with the node token; the rest with the caller's
// tailnet identity. The SPA answers everything else.
func routes(s services, users *auth.Store, nw network) http.Handler {
	router := whttp.NewRouter()
	router.Use(withLogger, withRecoverer)
	s.fleetH.NodeRoutes(router)
	s.keys.NodeRoutes(router)
	s.hooks.HookRoutes(router) // a forge inside the tailnet (Forgejo) calls them here
	router.Group(func(r *whttp.Router) {
		r.Use(auth.Middleware(nw.identify, users))
		r.Get("/api/me", auth.HandleMe)
		s.fleetH.UserRoutes(r)
		s.keys.UserRoutes(r)
		users.UserRoutes(r)
		workspace.NewHandler(s.workspace).Routes(r)
		settings.NewHandler(s.settings).Routes(r)
		app.NewHandler(s.app).Routes(r)
		database.NewHandler(s.database).Routes(r)
		project.NewHandler(s.project).Routes(r)
		s.hooks.UserRoutes(r)
		mcpRoutes(r, s)
	})
	router.Get("/", web.Handler())
	// Auth is by network identity, so the browser must not be a way in: CrossOriginProtection
	// refuses cross-site writes (CSRF from any page you visit), AllowHosts refuses other
	// names that resolve here (DNS rebinding). No request body needs more than 4 MB.
	return whttp.AllowHosts(http.NewCrossOriginProtection().Handler(http.MaxBytesHandler(router, 4<<20)), nw.hosts...)
}

func mcpRoutes(r *whttp.Router, s services) {
	h := mcp.Handler(s.fleet, s.workspace, s.app, s.database, s.project).ServeHTTP
	r.Post("/mcp", h)
	r.Get("/mcp", h) // 405 from the SDK (stateless), not the SPA's index.html
	r.Delete("/mcp", h)
}

func withLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, req)
		slog.Info("HTTP", "method", req.Method, "path", req.URL.Path, "duration", time.Since(start))
	})
}

func withRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("PANIC in HTTP", "error", fmt.Sprint(err), "trace", string(debug.Stack()))
				whttp.ErrorJSON(w, http.StatusInternalServerError, "Internal Server Error")
			}
		}()
		next.ServeHTTP(w, req)
	})
}

// serveHooks publishes /hooks/* alone on the internet with Tailscale Funnel, so a forge outside
// the tailnet (GitHub, GitLab.com) can reach them. The UI, the API and MCP are never there.
func serveHooks(ctx context.Context, s services, nw network) {
	if nw.funnel == nil {
		s.hooks.SetStatus(hooks.Status{URL: nw.url, Error: "--dev: no Funnel, hooks only on " + nw.url})
		return
	}
	ln, url, err := nw.funnel()
	if err != nil {
		slog.Warn("Webhooks not published: Funnel is off for this node", "err", err)
		s.hooks.SetStatus(hooks.Status{Error: "Tailscale Funnel is off for the hub: turn on HTTPS in the admin console and give the hub the funnel attribute in the tailnet policy. " + err.Error()})
		return
	}
	r := whttp.NewRouter()
	s.hooks.HookRoutes(r)
	srv := &http.Server{Handler: http.MaxBytesHandler(r, 1<<20), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	s.hooks.SetStatus(hooks.Status{URL: url})
	slog.Info("Webhooks published with Funnel", "url", url+"/hooks/")
	srv.Serve(ln)
}
