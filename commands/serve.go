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

	//goappctl:db
	// Register the database driver(s) at the composition root so database/sql has
	// them before any service Starts. Blank imports must stay inside this marker:
	// goimports cannot drop them, so removing the db component would otherwise
	// leave an import of a deleted package.
	_ "github.com/millken/goapp-template/internal/driver"
	//goappctl:end
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
// order, fills the service container, wires controllers onto the engine, and
// serves. Stop runs in reverse order via defer.
//
// Composition is deliberately one component per block: the container is built
// empty and each component assigns its own field, so a component reads only
// fields (svc.DB, svc.Session) and never another component's local variable.
// That is what makes an optional component removable as a contiguous block.
func runServe(cmd *cobra.Command, cfg *config.Config) error {
	log := slog.Default()

	// Service container: empty, then filled by the infrastructure blocks below.
	// Comments here are deliberately unnumbered — an optional block may be
	// absent, and numbered steps would read as if one went missing.
	svc := app.NewServices(log)

	//goappctl:db
	// Infrastructure is constructed and Started in dependency order: db first,
	// since session may store sessions in it.
	dbSvc := db.New(cfg.DB)
	if err := dbSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start db: %w", err)
	}
	defer func() { _ = dbSvc.Stop(context.Background()) }()
	svc.DB = dbSvc.DB()
	//goappctl:end

	//goappctl:session
	// svc.DB is nil without the db component, which is exactly the memory-store
	// fallback — no reference to the db block's dbSvc escapes it.
	sessSvc := session.New(cfg.Session, svc.DB)
	if err := sessSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer func() { _ = sessSvc.Stop(context.Background()) }()
	svc.Session = sessSvc
	//goappctl:end

	// HTTP engine (inertia).
	eng, mode, err := server.New(cfg.Server)
	if err != nil {
		return err
	}

	// Global middleware, then generated route wiring.
	//goappctl:session
	eng.Use(sessSvc.Middleware()) // session on every request
	//goappctl:end
	controller.MountAll(eng, svc) // generated non-admin areas

	//goappctl:admin
	// Admin area (needs its own config): validate, then mount with auth.
	adm := admin.New(svc, cfg.Admin)
	if err := adm.Validate(); err != nil {
		return err
	}
	adm.Mount(eng)
	//goappctl:end

	// Surface duplicate-route registration before binding a listener.
	if err := eng.RegistrationError(); err != nil {
		return fmt.Errorf("route registration: %w", err)
	}

	// Serve (inertia owns signals + HTTP graceful shutdown).
	slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
	serveErr := eng.Serve()
	_ = eng.Close()
	return serveErr
}
