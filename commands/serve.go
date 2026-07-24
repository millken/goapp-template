package commands

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/goapp-template/internal/module/db"
	"github.com/millken/goapp-template/internal/module/session"
	"github.com/millken/goapp-template/server"
	"github.com/spf13/cobra"

	// Register the database driver(s) used by feature modules. The blank import
	// lives at the composition root so database/sql has the driver registered
	// before any module Boots.
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

// runServe is the composition root for the serve command:
//
//	server.New → assemble the engine (mode/SSR/staticFS/rootHTML/middleware)
//	app.New    → wrap the engine in the lifecycle kernel
//	app.Use    → register modules (sample routes first; feature modules later)
//	app.Serve  → Boot → eng.Serve → Shutdown
func runServe(cmd *cobra.Command, cfg *config.Config) error {
	eng, mode, err := server.New(cfg.Server)
	if err != nil {
		return err
	}

	a, err := app.New(eng, app.WithShutdownTimeout(10*time.Second))
	if err != nil {
		return err
	}

	// Registration order = Boot order. Construct feature modules here and append
	// them after the sample routes. Each module is constructed unconditionally
	// and Use'd; its Boot enforces the enable-consistency rule (§4.4): a nil
	// config section yields a clear error rather than a silent skip. To disable
	// a module, comment out both its New and its Use entry below.
	dbMod := db.New(cfg.DB)
	// session depends on db (when store=db), so it is constructed with dbMod and
	// placed after db in Use order — db Boots first, so session's Boot can
	// resolve DB(). session's middleware wraps any later module's handlers
	// (e.g. admin in a future phase) per the §4.2 ordering guarantee.
	sessMod := session.New(cfg.Session, dbMod)

	// Order matters: routes → db → session → (future: admin). Each later module
	// may depend on earlier ones.
	if err := a.Use(server.NewRoutes(), dbMod, sessMod); err != nil {
		return fmt.Errorf("register modules: %w", err)
	}

	slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
	// ctx is main's signal context (SIGINT+SIGTERM), threaded through cobra; it
	// is consumed by Boot so a slow startup can be interrupted. eng.Serve owns
	// HTTP signal handling; app.Serve builds a fresh timeout ctx for Shutdown.
	return a.Serve(cmd.Context())
}
