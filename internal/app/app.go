// Package app is the thin lifecycle kernel. It wraps an already-assembled
// *inertia.Engine (assembled by server.New on the template side) and provides
// Module registration plus Boot/Serve/Shutdown lifecycle.
//
// The kernel only imports inertia (+ stdlib): it never imports config, so an
// import cycle via config→db→app→inertia is structurally impossible.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/millken/inertia"
)

// defaultShutdownTimeout mirrors inertia's own default so a stuck Shutdown hook
// cannot keep the process alive indefinitely.
const defaultShutdownTimeout = 10 * time.Second

// App wraps an externally-assembled engine and drives Module lifecycle.
// It holds no business services and reads no configuration.
type App struct {
	// Engine is the assembled engine (assembled by server.New). Modules use it
	// to register routes/middleware.
	Engine *inertia.Engine
	// Logger is the process-wide logger (slog.Default by default).
	Logger *slog.Logger

	mods             []Module
	shutdownTimeout  time.Duration
}

// Option configures the App.
type Option func(*App)

// WithShutdownTimeout sets the timeout applied to the Shutdown phase. If unset,
// defaultShutdownTimeout is used.
func WithShutdownTimeout(d time.Duration) Option {
	return func(a *App) { a.shutdownTimeout = d }
}

// WithLogger sets the App logger. If unset, slog.Default() is used.
func WithLogger(l *slog.Logger) Option {
	return func(a *App) { a.Logger = l }
}

// New wraps an already-assembled engine. Engine creation is done by server.New
// (template side), not here — so app never imports config and a cycle cannot
// form.
func New(eng *inertia.Engine, opts ...Option) (*App, error) {
	if eng == nil {
		return nil, errors.New("app: New requires a non-nil engine")
	}
	a := &App{
		Engine:           eng,
		Logger:           slog.Default(),
		shutdownTimeout:  defaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Use registers modules: it calls each Module.Register (attaching
// routes/middleware) and collects Booter/Shutdowner hooks by assertion.
//
// Register failure returns the error immediately. There is no rollback: the
// engine exposes no de-registration API, and on failure the process exits
// before Serve, so anything attached is unreachable (see §4.5).
func (a *App) Use(mods ...Module) error {
	for _, m := range mods {
		if m == nil {
			continue
		}
		if err := m.Register(a); err != nil {
			return err
		}
		a.mods = append(a.mods, m)
	}
	return nil
}

// Serve runs the full lifecycle:
//
//  1. Boot every Booter in registration order, using ctx (so a slow connect
//     can be interrupted by Ctrl-C). On failure, already-Booted modules are
//     shut down in reverse order and the error is returned.
//  2. eng.Serve() blocks; the engine owns signal handling (SIGINT+SIGTERM) and
//     HTTP graceful shutdown.
//  3. After eng.Serve returns, Shutdown hooks run in reverse order under a
//     fresh timeout context (the engine's internal signal ctx is already
//     stopped by this point, so the incoming ctx cannot be reused), then the
//     SSR VM is released via eng.Close.
//
// ctx is the signal context built in main and threaded through cobra; it is
// consumed only by Boot.
func (a *App) Serve(ctx context.Context) error {
	// 1. Boot (registration order), interruptible via ctx.
	booted := 0
	for _, m := range a.mods {
		b, ok := m.(Booter)
		if !ok {
			booted++
			continue
		}
		if err := b.Boot(ctx); err != nil {
			// Reverse-shutdown only the modules that already Booted, under a
			// fresh timeout ctx. The failing module is excluded: by convention a
			// Boot that fails partway cleans up its own partial state (e.g. db
			// closes the pool on ping failure), so it must not be Shutdown again.
			// `booted` has not been incremented for the failing module, so
			// a.mods[:booted] is exactly the successfully-Booted prefix.
			shutdownErr := a.shutdownN(context.Background(), booted)
			return errors.Join(err, shutdownErr)
		}
		booted++
	}

	// 2. Serve (engine owns signals + HTTP graceful shutdown).
	serveErr := a.Engine.Serve()

	// 3. Shutdown (reverse order) under a fresh timeout ctx, then release SSR VM.
	shutdownErr := a.shutdownN(context.Background(), len(a.mods))
	_ = a.Engine.Close()
	return errors.Join(serveErr, shutdownErr)
}

// shutdownN runs Shutdowner hooks over a.mods[:n] in reverse registration
// order under ctx. n bounds which modules are shut down: on the normal exit
// path it is len(a.mods); on a Boot-failure path it is the count of modules
// that successfully Booted (excluding the failing one).
//
// ctx should be a fresh context (typically with a timeout), never the original
// signal context — the engine stops its internal signal ctx before Serve
// returns, so the incoming context may already be cancelled.
func (a *App) shutdownN(ctx context.Context, n int) error {
	ctx, cancel := context.WithTimeout(ctx, a.shutdownTimeout)
	defer cancel()

	var errs error
	for i := n - 1; i >= 0; i-- {
		s, ok := a.mods[i].(Shutdowner)
		if !ok {
			continue
		}
		if err := s.Shutdown(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
