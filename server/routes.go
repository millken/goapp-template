package server

import "github.com/millken/inertia"

func registerRoutes(eng *inertia.Engine) {
	// Pages
	eng.GET("/", func(ctx *inertia.Context) {
		ctx.Set("message", "Welcome to goapp-template")
		_ = ctx.Render("Home")
	})

	// API example
	eng.GET("/api/health", func(ctx *inertia.Context) {
		_ = ctx.JSON(map[string]string{"status": "ok"})
	})
}
