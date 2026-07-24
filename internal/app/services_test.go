package app

import (
	"log/slog"
	"testing"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/service/session"

	_ "github.com/millken/goapp-template/internal/driver"
)

func TestNewServices_WiresFields(t *testing.T) {
	log := slog.Default()
	db, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sess := session.New(&session.Config{Secret: "k", Store: session.StoreMemory}, nil)

	svc := NewServices(log, db, sess)

	if svc.Log != log {
		t.Error("Log not wired")
	}
	if svc.DB != db {
		t.Error("DB not wired")
	}
	if svc.Session != sess {
		t.Error("Session not wired")
	}
}
