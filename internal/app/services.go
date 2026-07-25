package app

import (
	"log/slog"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/service/session"
)

// Services is the typed, read-only container for process-lifetime services,
// assembled once after infrastructure Start and shared by every controller.
// Per-request state lives on *inertia.Context, never here.
//
// It intentionally holds no *config.Config: config imports app's dependents,
// so an app→config edge would close an import cycle — pass config values as
// constructor arguments instead.
type Services struct {
	Log     *slog.Logger
	DB      *sqldb.DB        // resolved handle after Start; never nil in production
	Session *session.Service // provides Session(ctx) per request
}

// NewServices builds the container from already-Started infrastructure.
func NewServices(log *slog.Logger, db *sqldb.DB, sess *session.Service) *Services {
	return &Services{Log: log, DB: db, Session: sess}
}
