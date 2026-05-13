package server

import (
	"log/slog"

	"github.com/millken/inertia"
)

func registerRoutes(eng *inertia.Engine) {
	eng.GET("/", func(ctx *inertia.Context) {
		ctx.Set("message", "Welcome to goapp-template")
		if err := ctx.Render("Home"); err != nil {
			slog.Error("render Home", "err", err)
		}
	})

	eng.GET("/api/health", func(ctx *inertia.Context) {
		if err := ctx.JSON(map[string]string{"status": "ok"}); err != nil {
			slog.Error("json health", "err", err)
		}
	})
}
