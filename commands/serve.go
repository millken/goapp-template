package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/goapp-template/internal/controller"
	"github.com/millken/goapp-template/internal/controller/admin"
	"github.com/millken/goapp-template/internal/service/db"
	"github.com/millken/goapp-template/internal/service/session"
	"github.com/millken/goapp-template/server"
	"github.com/spf13/cobra"

	// Register the database driver(s) used by the app. The blank import lives at
	// the composition root so database/sql has the driver registered before any
	// service Starts.
	_ "github.com/millken/goapp-template/internal/driver"
)

func newServeCmd() *cobra.Command {
	var addr string
	var devAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// appCfg is populated by AppInit (PersistentPreRunE). Env overrides
			// (e.g. VITE_DEV_ADDR) are already applied there; this layer only
			// handles flags.
			cfg := appCfg
			if addr != "" {
				cfg.Server.Addr = addr
			}
			if devAddr != "" {
				cfg.Server.DevAddr = devAddr
			}
			return runServe(cmd, &cfg)
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", "", "Listen address (overrides config, e.g. :9090)")
	cmd.Flags().StringVar(&devAddr, "dev-addr", "", "Vite dev server URL (overrides config, e.g. http://localhost:5174)")
	return cmd
}

// runServe is the composition root for the serve command (the OpenCart-style
// index.php): it Starts infrastructure in dependency order, builds the typed
// service container, wires controllers onto the inertia engine, and serves.
//
// There is no Module abstraction: db/session are infrastructure services driven
// explicitly here; controllers are plain handler methods wired by the generated
// controller.MountAll (plus the admin area, wired separately because it needs
// its own config). Stop runs in reverse order via defer.
func runServe(cmd *cobra.Command, cfg *config.Config) error {
	log := slog.Default()

	// 1. Infrastructure: construct + Start in dependency order (db before
	//    session, which may use the db store).
	dbSvc := db.New(cfg.DB)
	if err := dbSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start db: %w", err)
	}
	defer func() { _ = dbSvc.Stop(context.Background()) }()

	sessSvc := session.New(cfg.Session, dbSvc)
	if err := sessSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer func() { _ = sessSvc.Stop(context.Background()) }()

	// 2. Typed service container (built from Started infra; DB is non-nil).
	svc := app.NewServices(log, dbSvc.DB(), sessSvc)

	// 3. HTTP engine (inertia).
	eng, mode, err := server.New(cfg.Server)
	if err != nil {
		return err
	}

	// 4. Global middleware + generated route wiring.
	eng.Use(sessSvc.Middleware()) // session on every request
	controller.MountAll(eng, svc) // generated non-admin controller areas

	// 5. Admin area (needs its own config): validated, then wired with auth.
	adm := admin.New(svc, cfg.Admin)
	if err := adm.Validate(); err != nil {
		return err
	}
	adm.Mount(eng)

	// Surface any duplicate-route registration before binding a listener.
	if err := eng.RegistrationError(); err != nil {
		return fmt.Errorf("route registration: %w", err)
	}

	// 6. Serve (inertia owns signals + HTTP graceful shutdown).
	slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
	serveErr := eng.Serve()
	_ = eng.Close()
	return serveErr
}
