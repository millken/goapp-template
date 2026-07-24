package commands

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/goapp-template/server"
	"github.com/spf13/cobra"
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
			cfg := appCfg.Server
			if addr != "" {
				cfg.Addr = addr
			}
			if devAddr != "" {
				cfg.DevAddr = devAddr
			}
			return runServe(cmd, cfg)
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", "", "Listen address (overrides config, e.g. :9090)")
	cmd.Flags().StringVar(&devAddr, "dev-addr", "", "Vite dev server URL (overrides config, e.g. http://localhost:5174)")
	return cmd
}

// runServe is the composition root for the serve command:
//
//	server.New → assemble the engine (mode/SSR/staticFS/rootHTML/middleware)
//	app.New    → wrap the engine in the lifecycle kernel
//	app.Use    → register modules (sample routes first; feature modules later)
//	app.Serve  → Boot → eng.Serve → Shutdown
func runServe(cmd *cobra.Command, cfg config.ServerConfig) error {
	eng, mode, err := server.New(cfg)
	if err != nil {
		return err
	}

	a, err := app.New(eng, app.WithShutdownTimeout(10*time.Second))
	if err != nil {
		return err
	}

	// Registration order = Boot order. Phase 1 wires only the sample routes
	// Module; phase 2+ appends feature modules (db, session, admin…) here.
	if err := a.Use(server.NewRoutes()); err != nil {
		return fmt.Errorf("register modules: %w", err)
	}

	slog.Info("server starting", "addr", cfg.Addr, "mode", mode, "dev_addr", cfg.DevAddr)
	// ctx is main's signal context (SIGINT+SIGTERM), threaded through cobra; it
	// is consumed by Boot so a slow startup can be interrupted. eng.Serve owns
	// HTTP signal handling; app.Serve builds a fresh timeout ctx for Shutdown.
	return a.Serve(cmd.Context())
}
