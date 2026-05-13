package commands

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/goapp-template/server"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(resolveConfigFile(cmd))
			if err != nil {
				slog.Warn("failed to load config", "err", err)
			}
			if addr != "" {
				cfg.Server.Addr = addr
			}
			return runServer(cmd.Context(), cfg.Server)
		},
	}
	cmd.Flags().StringVarP(&addr, "addr", "a", "", "Listen address (overrides config, e.g. :9090)")
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

	slog.Info("server starting", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
