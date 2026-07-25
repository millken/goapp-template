package app

import (
	"log/slog"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/service/session"
)

// Services is the typed container for process-lifetime services, assembled once
// after infrastructure Start and shared by every controller. Per-request state
// lives on *inertia.Context, never here.
//
// It intentionally holds no *config.Config: config imports app's dependents, so
// an app→config edge would close an import cycle — pass config values as
// constructor arguments instead.
//
// Every field beyond Log is filled in after construction, one component at a
// time (see commands/serve.go). This is what lets an optional component be
// removed without touching its consumers: a stripped component leaves its field
// nil instead of leaving a dangling reference to a deleted package.
type Services struct {
	Log *slog.Logger

	// DB is the resolved database handle, or nil when the db component is absent
	// (and before its Start). It stays a core field even in a db-less build
	// because *sqldb.DB is a third-party type — no component owns it.
	DB *sqldb.DB

	//goappctl:session
	// Session provides Session(c) per request.
	//
	// Unlike DB, this field's type comes from an optional component, so the
	// field itself disappears in a session-less build. Therefore: only the
	// session and admin areas may reference svc.Session — core code must not,
	// or it will fail to compile without the session component.
	Session *session.Service
	//goappctl:end
}

// NewServices returns a container holding only Log; infrastructure assigns the
// remaining fields after it Starts.
func NewServices(log *slog.Logger) *Services {
	return &Services{Log: log}
}
