package server

import (
	"log/slog"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

// Routes is an app.Module that registers the template's sample business routes.
// It demonstrates the Module contract: routes are attached at app.Use time
// rather than inside server.New, keeping engine assembly and route wiring
// separate.
type Routes struct{}

// NewRoutes returns the sample-routes Module.
func NewRoutes() Routes { return Routes{} }

// Register attaches the sample routes to the App's engine.
func (Routes) Register(a *app.App) error {
	eng := a.Engine

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

	return nil
}
