package hub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/agent"
	"github.com/jefjesuswt/walroos/internal/app"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/mcp"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/internal/sshkeys"
	"github.com/jefjesuswt/walroos/internal/web"
	"github.com/jefjesuswt/walroos/internal/workspace"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"github.com/spf13/cobra"
	"tailscale.com/net/netns"
	"tailscale.com/tsnet"
)

const devLogin = "dev@localhost"

var (
	dataDir  string
	hostname string
	owner    string
	dev      bool
	devAddr  string
)

var Cmd = &cobra.Command{
	Use:   "hub",
	Short: "Starts Walroos in Hub mode (control plane: API, UI, database)",
	Long: `Starts the hub on the tailnet as <hostname> (default walroos-hub).

First run needs a Tailscale auth key (TS_AUTHKEY) or an interactive login (the URL is printed),
and --owner with your tailnet login so you can use the UI.

--dev skips Tailscale entirely: listens on localhost and treats every request as ` + devLogin + `.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		ui.PrintTitle("Walroos Hub")

		db, err := config.NewSQLite(filepath.Join(dataDir, "hub.db"))
		if err != nil {
			return ui.WrapError("open database: %w", err)
		}
		defer db.Close()

		if err := db.Migrate(ctx); err != nil {
			return ui.WrapError("migrate database: %w", err)
		}

		users := auth.NewStore(db)
		if dev {
			owner = devLogin
		}
		if owner != "" {
			if err := users.EnsureOwner(ctx, owner); err != nil {
				return ui.WrapError("add owner: %w", err)
			}
		}
		if n, err := users.Count(ctx); err != nil {
			return ui.WrapError("count users: %w", err)
		} else if n == 0 {
			return ui.WrapError("no users yet: start once with --owner <your tailnet login>, e.g. --owner you@github")
		}

		var (
			ln       net.Listener
			identify auth.Identify
			hubURL   string
			hubID    string                  // our tailnet StableNodeID; agents only accept calls from it
			hosts    = []string{"localhost"} // Host names we answer to, besides IPs (DNS rebinding)
			agents   = &agent.Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
		)

		if dev {
			slog.Warn("DEV MODE: no Tailscale, no auth. Never expose this port.", "addr", devAddr, "user", devLogin)
			ln, err = net.Listen("tcp", devAddr)
			if err != nil {
				return ui.WrapError("listen: %w", err)
			}
			identify = auth.Static(devLogin)
			hubURL = "http://" + devAddr
			hubID = "dev" // ponytail: no tailnet in --dev, so agents can't verify or be reached
		} else {
			// tsnet is userspace-only (no TUN, no kernel routes), so its sockets don't need to
			// bypass the host's routing. Bypassing (SO_MARK / SO_BINDTODEVICE to the default
			// interface) skips host VPNs like NordVPN, whose firewall then drops the traffic.
			// Same as `tailscaled --tun=userspace-networking`.
			netns.SetEnabled(false)
			ts := &tsnet.Server{
				Hostname: hostname,
				Dir:      filepath.Join(dataDir, "tsnet"),
				AuthKey:  authKey(),
				Logf:     func(string, ...any) {}, // backend noise; UserLogf still prints the login URL
			}
			defer ts.Close()

			slog.Info("Connecting to the tailnet...", "hostname", hostname)
			status, err := ts.Up(ctx)
			if err != nil {
				return ui.WrapError("tailscale up: %w", err)
			}
			lc, err := ts.LocalClient()
			if err != nil {
				return ui.WrapError("tailscale local client: %w", err)
			}
			// ponytail: plain HTTP on :80 is already encrypted by WireGuard; ListenTLS(:443) when browsers need https (clipboard API, etc.)
			ln, err = ts.Listen("tcp", ":80")
			if err != nil {
				return ui.WrapError("tailscale listen: %w", err)
			}
			identify = auth.Tailscale(lc)
			hubURL = "http://" + hostname
			hubID = string(status.Self.ID)
			hosts = []string{hostname, strings.TrimSuffix(status.Self.DNSName, ".")}
			// The full MagicDNS name and IP work when the short name doesn't resolve (phones
			// with "Use Tailscale DNS" off, other search domains).
			slog.Info("On the tailnet", "dns", strings.TrimSuffix(status.Self.DNSName, "."), "ips", status.TailscaleIPs)
			agents.HTTP = ts.HTTPClient()
			agents.HTTP.Timeout = 30 * time.Second
		}

		fleetSvc := fleet.NewService(fleet.NewStore(db))
		fleetHandler := fleet.NewHandler(fleetSvc, hubURL, hubID, agents)
		keysHandler := sshkeys.NewHandler(sshkeys.NewStore(db), fleetSvc.Authenticate)
		keyPath := filepath.Join(dataDir, "secret.key")
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			// A new key can't open the old values: deploys would run without their secrets.
			var sealed int
			db.Read.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM workspace_env) + (SELECT count(*) FROM app_env)`).Scan(&sealed)
			if sealed > 0 {
				return ui.WrapError("%s is missing but the database has %d encrypted variables: restore the key next to hub.db", keyPath, sealed)
			}
		}
		box, err := secretbox.LoadOrCreate(keyPath)
		if err != nil {
			return ui.WrapError("workspace env key: %w", err)
		}
		wsSvc := workspace.NewService(workspace.NewStore(db), fleetSvc, agents, box)
		wsHandler := workspace.NewHandler(wsSvc)
		appSvc := app.NewService(app.NewStore(db), fleetSvc, agents, box)
		go appSvc.Run(ctx)

		router := whttp.NewRouter()
		router.Use(withLogger, withRecoverer)
		fleetHandler.NodeRoutes(router)
		keysHandler.NodeRoutes(router)
		router.Group(func(r *whttp.Router) {
			r.Use(auth.Middleware(identify, users))
			r.Get("/api/me", auth.HandleMe)
			fleetHandler.UserRoutes(r)
			keysHandler.UserRoutes(r)
			wsHandler.Routes(r)
			app.NewHandler(appSvc).Routes(r)
			mcpHandler := mcp.Handler(fleetSvc, wsSvc, appSvc).ServeHTTP
			r.Post("/mcp", mcpHandler)
			r.Get("/mcp", mcpHandler) // 405 from the SDK (stateless), not the SPA's index.html
			r.Delete("/mcp", mcpHandler)
		})
		router.Get("/", web.Handler())

		// Auth is by network identity, so the browser must not be a way in: CrossOriginProtection
		// refuses cross-site writes (CSRF from any page you visit), AllowHosts refuses other
		// names that resolve here (DNS rebinding). No request body needs more than 4 MB.
		handler := whttp.AllowHosts(http.NewCrossOriginProtection().Handler(http.MaxBytesHandler(router, 4<<20)), hosts...)
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			srv.Shutdown(shutdownCtx)
		}()

		slog.Info("Hub online", "url", hubURL)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return ui.WrapError("server crashed: %w", err)
		}
		slog.Info("Hub stopped")
		return nil
	},
}

func init() {
	cfgDir, _ := os.UserConfigDir()
	Cmd.Flags().StringVar(&dataDir, "data", filepath.Join(cfgDir, "walroos"), "Directory for hub.db and Tailscale state")
	Cmd.Flags().StringVar(&hostname, "hostname", "walroos-hub", "Hostname on the tailnet")
	Cmd.Flags().StringVar(&owner, "owner", "", "Tailnet login allowed as owner (e.g. you@github); needed on first run")
	Cmd.Flags().BoolVar(&dev, "dev", false, "Local development: no Tailscale, no auth")
	Cmd.Flags().StringVar(&devAddr, "addr", "127.0.0.1:8080", "Listen address in --dev mode")
}

func authKey() string {
	if k := os.Getenv("TS_AUTHKEY"); k != "" {
		return k
	}
	return os.Getenv("TS_AUTH_KEY") // name used by earlier walroos versions
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
