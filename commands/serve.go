package commands

import (
	"context"
	"fmt"
	"log/slog"
	//goappctl:queue
	// time is needed only by queueStopTimeout below; if this component is
	// stripped, both go together.
	"time"
	//goappctl:end
	//goappctl:storage
	// io/fs, net/http and strings are needed only by storageFileServer below;
	// if this component is stripped, both go together.
	"io/fs"
	"net/http"
	"strings"
	//goappctl:end

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/goapp-template/internal/controller"
	"github.com/millken/goapp-template/internal/controller/admin"
	"github.com/millken/goapp-template/internal/service/db"
	//goappctl:queue
	"github.com/millken/goapp-template/internal/service/queue"
	"github.com/millken/goapp-template/internal/tasks"
	//goappctl:end
	"github.com/millken/goapp-template/internal/service/session"
	//goappctl:storage
	// inertia is needed here only for the static route below; if this
	// component is stripped, both go together, or the import would dangle.
	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
	//goappctl:end
	"github.com/millken/goapp-template/server"
	"github.com/spf13/cobra"

	//goappctl:db
	// Register the database driver(s) at the composition root so database/sql has
	// them before any service Starts. Blank imports must stay inside this marker:
	// goimports cannot drop them, so removing the db component would otherwise
	// leave an import of a deleted package.
	_ "github.com/millken/goapp-template/internal/driver"
	//goappctl:end
)

//goappctl:queue

// queueStopTimeout bounds how long shutdown waits for the queue.
//
// It has to exceed queue.shutdown_grace, since Stop spends that waiting for
// in-flight tasks and then a little longer after cancelling them. Generous rather
// than tight: this is a backstop for a handler that ignores its context, and Stop
// still drains for shutdown_grace and no longer — so raising this does not slow
// shutdown down, and lowering it below shutdown_grace would quietly shorten the
// drain the operator configured.
const queueStopTimeout = 2 * time.Minute

//goappctl:end

func newServeCmd() *cobra.Command {
	var addr string
	var devAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// appCfg is populated by AppInit; this layer only applies flags.
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

// runServe is the composition root: it Starts infrastructure in dependency
// order, fills the service container, wires controllers onto the engine, and
// serves. Stop runs in reverse order via defer.
//
// Composition is deliberately one component per block: the container is built
// empty and each component assigns its own field, so a component reads only
// fields (svc.DB, svc.Session) and never another component's local variable.
// That is what makes an optional component removable as a contiguous block.
func runServe(cmd *cobra.Command, cfg *config.Config) error {
	log := slog.Default()

	// Service container: empty, then filled by the infrastructure blocks below.
	// Comments here are deliberately unnumbered — an optional block may be
	// absent, and numbered steps would read as if one went missing.
	svc := app.NewServices(log)

	//goappctl:db
	// Infrastructure is constructed and Started in dependency order: db first,
	// since session may store sessions in it.
	dbSvc := db.New(cfg.DB)
	if err := dbSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start db: %w", err)
	}
	defer func() { _ = dbSvc.Stop(context.Background()) }()
	svc.DB = dbSvc.DB()
	//goappctl:end

	//goappctl:session
	// svc.DB is nil without the db component, which is exactly the memory-store
	// fallback — no reference to the db block's dbSvc escapes it.
	sessSvc := session.New(cfg.Session, svc.DB)
	if err := sessSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer func() { _ = sessSvc.Stop(context.Background()) }()
	svc.Session = sessSvc
	//goappctl:end

	//goappctl:storage
	// Independent of db and session: it owns a directory, nothing else.
	storSvc := storage.New(cfg.Storage)
	if err := storSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start storage: %w", err)
	}
	defer func() { _ = storSvc.Stop(context.Background()) }()
	svc.Storage = storSvc
	//goappctl:end

	//goappctl:queue
	// Last of the service blocks, for two reasons that both matter.
	//
	// A task handler may read any field of svc, and Start launches the worker — so
	// every field has to be filled before it runs. And defer is LIFO, so registering
	// last means the queue is the FIRST thing stopped: the worker must be gone before
	// db.Stop closes the pool underneath it.
	//
	// Two details specific to this block:
	//
	//   * svc.Queue is assigned BEFORE Start, unlike every other component. Start
	//     starts running tasks, and a handler that enqueues follow-up work reads
	//     svc.Queue — assigning afterwards is a nil dereference racing a goroutine.
	//   * The migrations table comes from the db service rather than [queue]. The
	//     queue records its own schema under its own migration service name, so it can
	//     be added to a project whose schema has already moved on; the table it writes
	//     that row in still has to be the db component's. No [db.migrations] section
	//     means no queue migration either.
	queueReg := queue.NewRegistry()
	var queueOpts []queue.Option
	if dbSvc.MigrationsEnabled() {
		queueOpts = append(queueOpts, queue.WithMigrations(dbSvc.MigrationTable()))
	}
	queueSvc := queue.New(cfg.Queue, svc.DB, queueReg, log, queueOpts...)
	svc.Queue = queueSvc
	tasks.Register(queueReg, svc)
	if err := queueSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start queue: %w", err)
	}
	// Unlike the other Stop calls, this one gets a deadline. The others pass a
	// background context because they release resources promptly; the queue may be
	// waiting on a handler. Stop drains for queue.shutdown_grace and then cancels, so
	// this deadline is only the backstop for a handler that ignores the cancellation
	// — it is not what sizes the drain.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), queueStopTimeout)
		defer cancel()
		if err := queueSvc.Stop(stopCtx); err != nil {
			slog.Warn("queue did not stop cleanly", "err", err)
		}
	}()
	//goappctl:end

	// HTTP engine (inertia).
	eng, mode, err := server.New(cfg.Server)
	if err != nil {
		return err
	}

	// Global middleware, then generated route wiring.
	//goappctl:session
	eng.Use(sessSvc.Middleware()) // session on every request
	//goappctl:end
	//goappctl:storage
	// Not eng.StaticFS: that helper is a no-op in development mode, where dist
	// is Vite's job — but uploads must be readable in both modes.
	//
	// StaticFileServer trims its prefix against the raw request path (see its
	// doc comment), so the prefix must end in "/" the way inertia's own
	// e.StaticFS("/assets/", ...) convention does — the bare URLPrefix leaves
	// a leading "/" that no fs.FS accepts as a valid relative path.
	//
	// Wrapped rather than registered directly: fs.FS.Open answers
	// fs.ErrInvalid for a path fs.ValidPath rejects (a ".." segment survives
	// request parsing intact, encoded or not, since neither net/http nor the
	// router clean it), and fs.ErrInvalid is neither fs.ErrNotExist nor
	// fs.ErrPermission — so inertia's default handling falls through to a 500
	// and an ERROR log line for every malformed request a scanner sends.
	// storageFileServer answers 404 instead, same as a path that is merely
	// missing.
	//
	// Registered at both the bare prefix and the wildcard: the router's radix
	// tree keeps its own (unset) handler slot at the bare-prefix node, so a
	// request for exactly "/uploads/" never reaches the wildcard's handler at
	// all without the first registration — it hits a nil handler in the
	// router and 500s before storageFileServer ever runs.
	uploadsPrefix := storSvc.URLPrefix() + "/"
	uploadsServe := storageFileServer(uploadsPrefix, storSvc.FS())
	eng.GET(uploadsPrefix, uploadsServe)
	eng.GET(uploadsPrefix+"*", uploadsServe)
	//goappctl:end
	controller.MountAll(eng, svc) // generated non-admin areas

	//goappctl:admin
	// Admin area (needs its own config): validate, then mount with auth.
	adm := admin.New(svc, cfg.Admin)
	if err := adm.Validate(); err != nil {
		return err
	}
	adm.Mount(eng)
	//goappctl:end

	// Surface duplicate-route registration before binding a listener.
	if err := eng.RegistrationError(); err != nil {
		return fmt.Errorf("route registration: %w", err)
	}

	// Serve (inertia owns signals + HTTP graceful shutdown).
	slog.Info("server starting", "addr", cfg.Server.Addr, "mode", mode, "dev_addr", cfg.Server.DevAddr)
	serveErr := eng.Serve()
	_ = eng.Close()
	return serveErr
}

//goappctl:storage

// storageFileServer wraps inertia.StaticFileServer with a validity check on
// the path fs.FS.Open would receive, so a malformed request answers 404
// instead of an inertia 500-plus-ERROR-log-line. See the route registration
// above for why it is also worth registering at the bare prefix, not just the
// wildcard.
func storageFileServer(prefix string, fsys fs.FS) inertia.HandlerFunc {
	next := inertia.StaticFileServer(prefix, fsys)
	return func(c *inertia.Context) {
		p := strings.TrimPrefix(c.Request.URL.Path, prefix)
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		if !fs.ValidPath(p) {
			http.NotFound(c.Writer, c.Request)
			return
		}
		next(c)
	}
}

//goappctl:end
