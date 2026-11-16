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
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/fleet"
	"github.com/jefjesuswt/walroos/internal/web"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/jefjesuswt/walroos/shared/whttp"
	"github.com/spf13/cobra"
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
		)

		if dev {
			slog.Warn("DEV MODE: no Tailscale, no auth. Never expose this port.", "addr", devAddr, "user", devLogin)
			ln, err = net.Listen("tcp", devAddr)
			if err != nil {
				return ui.WrapError("listen: %w", err)
			}
			identify = auth.Static(devLogin)
			hubURL = "http://" + devAddr
		} else {
			ts := &tsnet.Server{
				Hostname: hostname,
				Dir:      filepath.Join(dataDir, "tsnet"),
				AuthKey:  authKey(),
				Logf:     func(string, ...any) {}, // backend noise; UserLogf still prints the login URL
			}
			defer ts.Close()

			slog.Info("Connecting to the tailnet...", "hostname", hostname)
			if _, err := ts.Up(ctx); err != nil {
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
		}

		fleetHandler := fleet.NewHandler(fleet.NewService(fleet.NewStore(db)), hubURL)

		router := whttp.NewRouter()
		router.Use(withLogger, withRecoverer)
		fleetHandler.NodeRoutes(router)
		router.Group(func(r *whttp.Router) {
			r.Use(auth.Middleware(identify, users))
			r.Get("/api/me", auth.HandleMe)
			fleetHandler.UserRoutes(r)
		})
		router.Get("/", web.Handler())

		srv := &http.Server{Handler: router, ReadHeaderTimeout: 10 * time.Second}
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
