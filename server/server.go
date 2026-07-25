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

//goappctl:ssr

// ssrBundleName is the SSR bundle filename, shared between the dev config path
// and the prod embed so the two cannot drift.
const ssrBundleName = "ssr-render-cjs.js"

//goappctl:end

// modeName maps the internal mode to a human-readable string.
func modeName(m inertia.Mode) string {
	switch m {
	//goappctl:ssr
	case inertia.ModeSSR:
		return "ssr"
	//goappctl:end
	case inertia.ModeDevelopment:
		return "development"
	default:
		return "production"
	}
}

// New assembles an inertia.Engine from config and returns it with the derived
// mode name (for logging). Route registration is done by the caller.
func New(cfg config.ServerConfig) (*inertia.Engine, string, error) {
	mode := defaultMode
	//goappctl:ssr
	if cfg.SSR {
		mode = inertia.ModeSSR
	}
	//goappctl:end

	assetsFS, err := staticFS(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("load static assets: %w", err)
	}

	opts := []inertia.Option{
		inertia.WithMode(mode),
		inertia.WithAddr(cfg.Addr),
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

	//goappctl:ssr
	if mode == inertia.ModeSSR {
		bundle, err := loadSSRBundle(cfg)
		if err != nil {
			return nil, "", fmt.Errorf("load SSR bundle: %w", err)
		}
		vm, err := quickjs.NewVM(
			ssr.WithDefaultCache(8),
			ssr.WithBundlerJS(bundle),
		)
		if err != nil {
			return nil, "", fmt.Errorf("create SSR VM: %w", err)
		}
		opts = append(opts, inertia.WithSSR(vm))
	}
	//goappctl:end

	eng, err := inertia.New(opts...)
	if err != nil {
		return nil, "", fmt.Errorf("create inertia engine: %w", err)
	}

	eng.Use(middleware.Gzip(), middleware.Recovery())

	eng.StaticFS("/", assetsFS)

	return eng, modeName(mode), nil
}

// firstAsset returns the first dist entry matching pattern, or "" after warning.
// A missing entry is not fatal: the page still renders, just without that tag.
func firstAsset(distFS fs.FS, pattern string) string {
	entries, err := fs.Glob(distFS, pattern)
	if err != nil || len(entries) == 0 {
		slog.Warn("rootHTML: no dist asset matches", "pattern", pattern)
		return ""
	}
	return entries[0]
}

// rootHTML scans dist for the entry CSS/JS and builds the root HTML template.
func rootHTML(distFS fs.FS) string {
	cssLink := ""
	if css := firstAsset(distFS, "assets/main-*.css"); css != "" {
		cssLink = fmt.Sprintf(`<link rel="stylesheet" href="/%s">`, css)
	}
	jsTag := ""
	if js := firstAsset(distFS, "assets/main-*.js"); js != "" {
		jsTag = fmt.Sprintf(`<script type="module" src="/%s"></script>`, js)
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
