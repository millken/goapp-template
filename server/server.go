package server

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/inertia"
	"github.com/millken/inertia/middleware"
	"github.com/millken/inertia/ssr"
	"github.com/millken/inertia/ssr/quickjs"
)

// New creates an inertia.Engine from application config and registers routes.
func New(cfg config.ServerConfig) (*inertia.Engine, error) {
	mode := defaultMode
	if cfg.SSR {
		mode = inertia.ModeSSR
	}

	assetsFS, err := staticFS(cfg)
	if err != nil {
		return nil, fmt.Errorf("load static assets: %w", err)
	}

	opts := []inertia.Option{
		inertia.WithMode(mode),
		inertia.WithDevAddr(cfg.DevAddr),
		inertia.WithErrorHandler(http.StatusNotFound, func(w http.ResponseWriter, r *http.Request, _ error) {
			http.Error(w, "404 Not Found", http.StatusNotFound)
		}),
		inertia.WithErrorHandler(http.StatusInternalServerError, func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("server error", "err", err)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		}),
	}

	if mode != inertia.ModeDevelopment {
		opts = append(opts, inertia.WithRootHTML(rootHTML(assetsFS)))
	}

	if mode == inertia.ModeSSR {
		bundle, err := loadSSRBundle(cfg)
		if err != nil {
			return nil, fmt.Errorf("load SSR bundle: %w", err)
		}
		vm, err := quickjs.NewVM(
			ssr.WithDefaultCache(8),
			ssr.WithBundlerJS(bundle),
		)
		if err != nil {
			return nil, fmt.Errorf("create SSR VM: %w", err)
		}
		opts = append(opts, inertia.WithSSR(vm))
	}

	eng, err := inertia.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create inertia engine: %w", err)
	}

	eng.Use(middleware.Gzip(), middleware.Recovery())

	eng.StaticFS("/", assetsFS)
	registerRoutes(eng)

	return eng, nil
}

// rootHTML scans dist for the entry CSS and JS files and builds a root HTML template.
func rootHTML(distFS fs.FS) string {
	cssLink := ""
	if entries, err := fs.Glob(distFS, "assets/main-*.css"); err == nil && len(entries) > 0 {
		cssLink = fmt.Sprintf(`<link rel="stylesheet" href="/%s">`, entries[0])
	} else {
		slog.Warn("rootHTML: no main CSS found in dist assets")
	}
	jsTag := ""
	if entries, err := fs.Glob(distFS, "assets/main-*.js"); err == nil && len(entries) > 0 {
		jsTag = fmt.Sprintf(`<script type="module" src="/%s"></script>`, entries[0])
	} else {
		slog.Warn("rootHTML: no main JS found in dist assets")
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <!--inertia-head-meta-inertia-->
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>App</title>
  %s
</head>
<body>
  <div id="app"><!--inertia-ssr-content-inertia--></div>
  <script>window.__INERTIA_PAGE_DATA__="<!--inertia-data-page-inertia-->";</script>
  %s
</body>
</html>`, cssLink, jsTag)
}
