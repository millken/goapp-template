// Package site holds the public non-CRUD routes (home, health) that don't fit
// the generated resource convention. Its Mount is hand-written and wired by the
// generated controller.MountAll.
package site

import (
	"log/slog"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

// Site is the controller for the public top-level routes.
type Site struct {
	*app.Services
}

// Mount wires the site routes. Hand-written (these are not CRUD routes); kept in
// the same shape as generated Mount funcs so controller.MountAll can call it.
func Mount(eng *inertia.Engine, svc *app.Services) {
	s := &Site{svc}
	eng.GET("/", s.Home)
	eng.GET("/api/health", s.Health)
}

// Home renders the landing page.
func (s *Site) Home(c *inertia.Context) {
	c.Set("message", "Welcome to goapp-template")
	if err := c.Render("Home"); err != nil {
		slog.Error("render Home", "err", err)
	}
}

// Health returns a JSON liveness response.
func (s *Site) Health(c *inertia.Context) {
	if err := c.JSON(map[string]string{"status": "ok"}); err != nil {
		slog.Error("json health", "err", err)
	}
}
