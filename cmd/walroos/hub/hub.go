package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jefjesuswt/walroos/config"
	"github.com/jefjesuswt/walroos/internal/auth"
	"github.com/jefjesuswt/walroos/internal/secretbox"
	"github.com/jefjesuswt/walroos/shared/ui"
	"github.com/spf13/cobra"
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
	RunE: func(cmd *cobra.Command, args []string) error { return run(cmd.Context()) },
}

func init() {
	cfgDir, _ := os.UserConfigDir()
	Cmd.PersistentFlags().StringVar(&dataDir, "data", filepath.Join(cfgDir, "walroos"), "Directory for hub.db and Tailscale state")
	Cmd.PersistentFlags().StringVar(&hostname, "hostname", "walroos-hub", "Hostname on the tailnet")
	Cmd.Flags().StringVar(&owner, "owner", "", "Tailnet login allowed as owner (e.g. you@github); needed on first run")
	Cmd.Flags().BoolVar(&dev, "dev", false, "Local development: no Tailscale, no auth")
	Cmd.Flags().StringVar(&devAddr, "addr", "127.0.0.1:8080", "Listen address in --dev mode")
	Cmd.AddCommand(installCmd, backupCmd, restoreCmd)
}

// run opens the database, joins the tailnet (or not, in --dev), wires the services and serves.
func run(ctx context.Context) error {
	ui.PrintTitle("Walroos Hub")
	db, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	users := auth.NewStore(db)
	if err := ensureOwner(ctx, users); err != nil {
		return err
	}
	nw, err := listen(ctx)
	if err != nil {
		return err
	}
	defer nw.close()
	box, err := openBox(ctx, db)
	if err != nil {
		return err
	}
	svc := newServices(ctx, db, box, nw)
	go serveHooks(ctx, svc, nw)
	return serve(ctx, nw, routes(svc, users, nw))
}

func openDB(ctx context.Context) (*config.Database, error) {
	db, err := config.NewSQLite(filepath.Join(dataDir, "hub.db"))
	if err != nil {
		return nil, ui.WrapError("open database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		db.Close()
		return nil, ui.WrapError("migrate database: %w", err)
	}
	return db, nil
}

// ensureOwner adds --owner (dev@localhost in --dev) and refuses to start with no user at all.
func ensureOwner(ctx context.Context, users *auth.Store) error {
	if dev {
		owner = devLogin
	}
	if owner != "" {
		if err := users.EnsureOwner(ctx, owner); err != nil {
			return ui.WrapError("add owner: %w", err)
		}
	}
	n, err := users.Count(ctx)
	if err != nil {
		return ui.WrapError("count users: %w", err)
	}
	if n == 0 {
		return ui.WrapError("no users yet: start once with --owner <your tailnet login>, e.g. --owner you@github")
	}
	return nil
}

// openBox loads the key that seals env values and tokens, refusing to make a new one when the
// database already holds values sealed with a lost key: deploys would run without their secrets.
func openBox(ctx context.Context, db *config.Database) (*secretbox.Box, error) {
	keyPath := filepath.Join(dataDir, "secret.key")
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		var sealed int
		db.Read.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM workspace_env) + (SELECT count(*) FROM app_env) + (SELECT count(*) FROM databases)`).Scan(&sealed)
		if sealed > 0 {
			return nil, ui.WrapError("%s is missing but the database has %d encrypted values: restore the key next to hub.db", keyPath, sealed)
		}
	}
	box, err := secretbox.LoadOrCreate(keyPath)
	if err != nil {
		return nil, ui.WrapError("workspace env key: %w", err)
	}
	return box, nil
}

func serve(ctx context.Context, nw network, handler http.Handler) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	slog.Info("Hub online", "url", nw.url)
	if err := srv.Serve(nw.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return ui.WrapError("server crashed: %w", err)
	}
	slog.Info("Hub stopped")
	return nil
}
