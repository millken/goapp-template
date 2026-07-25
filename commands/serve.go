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

	// Register the database driver(s) at the composition root so database/sql
	// has them before any service Starts.
	_ "github.com/millken/goapp-template/internal/driver"
)

func newServeCmd() *cobra.Command {
	var addr string
	var devAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// appCfg is populated by AppInit; this layer only applies flags.
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

// runServe is the composition root: it Starts infrastructure in dependency
// order, builds the service container, wires controllers onto the engine, and
// serves. db/session are driven explicitly here; controllers are wired by the
// generated controller.MountAll (plus the admin area, which needs its own
// config). Stop runs in reverse order via defer.
func runServe(cmd *cobra.Command, cfg *config.Config) error {
	log := slog.Default()

	// 1. Infrastructure: construct + Start in dependency order (db before session).
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

	// 2. Typed service container (built from Started infra).
	svc := app.NewServices(log, dbSvc.DB(), sessSvc)

	// 3. HTTP engine (inertia).
	eng, mode, err := server.New(cfg.Server)
	if err != nil {
		return err
	}

	// 4. Global middleware + generated route wiring.
	eng.Use(sessSvc.Middleware()) // session on every request
	controller.MountAll(eng, svc) // generated non-admin areas

	// 5. Admin area (needs its own config): validate, then mount with auth.
	adm := admin.New(svc, cfg.Admin)
	if err := adm.Validate(); err != nil {
		return err
	}
	adm.Mount(eng)

	// Surface duplicate-route registration before binding a listener.
	if err := eng.RegistrationError(); err != nil {
		return fmt.Errorf("route registration: %w", err)
	}

	// 6. Serve (inertia owns signals + HTTP graceful shutdown).
	slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
	serveErr := eng.Serve()
	_ = eng.Close()
	return serveErr
}
