package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

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
			appCfg, ok := configFromContext(cmd.Context())
			if !ok {
				return fmt.Errorf("config not found in context: AppInit must run before serve")
			}
			cfg := appCfg.Server
			if addr != "" {
				cfg.Addr = addr
			}
			// --dev-addr flag takes priority, then VITE_DEV_ADDR env var, then config
			if devAddr == "" {
				devAddr = os.Getenv("VITE_DEV_ADDR")
			}
			if devAddr != "" {
				cfg.DevAddr = devAddr
			}
			return runServer(cmd.Context(), cfg)
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", "", "Listen address (overrides config, e.g. :9090)")
	cmd.Flags().StringVar(&devAddr, "dev-addr", "", "Vite dev server URL (overrides config, e.g. http://localhost:5174)")
	return cmd
}

func runServer(ctx context.Context, cfg config.ServerConfig) error {
	eng, err := server.New(cfg)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           eng,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("server shutdown error", "err", err)
		}
	}()

	slog.Info("server starting", "addr", cfg.Addr, "mode", server.ModeName(cfg), "dev_addr", cfg.DevAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
