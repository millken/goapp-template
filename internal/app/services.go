package app

import (
	"log/slog"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/service/session"
)

// Services is the typed service container, assembled once after infrastructure
// Start and shared (read-only) by every controller. It replaces OpenCart's
// map-based $registry: same "one place to reach shared services" ergonomics,
// but every field is compile-time typed — no string keys, no `any`, no runtime
// type assertions, no missing-key panics.
//
// It holds ONLY process-lifetime services (safe to share across goroutines).
// Per-request state never lives here — it stays on *inertia.Context.
//
// Note it deliberately does NOT hold *config.Config: config imports the service
// and controller packages, and controllers import app, so an app→config edge
// would close an import cycle. Controllers that need a specific configuration
// value receive it as a typed field/argument at construction instead.
type Services struct {
	Log     *slog.Logger
	DB      *sqldb.DB        // already-opened handle (post-Start), never nil in production
	Session *session.Service // provides Session(ctx) per request
}

// NewServices builds the container from already-Started infrastructure. Because
// DB is the resolved handle (not a lazy provider), controllers never hit the
// "DB() before Start" panic path.
func NewServices(log *slog.Logger, db *sqldb.DB, sess *session.Service) *Services {
	return &Services{Log: log, DB: db, Session: sess}
}
