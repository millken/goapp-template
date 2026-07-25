package db

import (
	"context"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/app"

	// Register the SQLite driver for tests.
	_ "github.com/millken/goapp-template/internal/driver"
)

// newTestService builds a Service against an isolated in-memory SQLite DB with
// migrations enabled.
func newTestService(t *testing.T) *Service {
	t.Helper()
	return New(&Config{
		Driver: "sqlite3",
		DSN:    ":memory:",
		Migrations: &Migrations{
			Table:   "schema_migrations",
			Service: "test",
		},
	})
}

// TestDB_BeforeStartPanics verifies DB() panics before Start.
func TestDB_BeforeStartPanics(t *testing.T) {
	s := newTestService(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic before Start")
		}
	}()
	_ = s.DB()
}

// TestStart_OpensAndMigrates verifies Start runs the sample migration and DB()
// returns a usable handle with the version recorded.
func TestStart_OpensAndMigrates(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(ctx) })

	// The sample migration creates app_meta; verify it is writable.
	db := s.DB()
	if _, err := db.ExecContext(ctx, `INSERT INTO app_meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatalf("insert into migrated table: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = 'k'`).Scan(&got); err != nil {
		t.Fatalf("select from migrated table: %v", err)
	}
	if got != "v" {
		t.Fatalf("got %q, want %q", got, "v")
	}

	// The migrations table should record the applied version.
	var version string
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE service = 'test'`).Scan(&version); err != nil {
		t.Fatalf("select migration version: %v", err)
	}
	if version == "" {
		t.Fatalf("expected non-empty migration version, got %q", version)
	}
}

// TestStart_NilConfig verifies a Started service with no config section fails.
func TestStart_NilConfig(t *testing.T) {
	s := New(nil)
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for nil config, got nil")
	}
	if !strings.Contains(err.Error(), "config section missing") {
		t.Fatalf("expected 'config section missing' error, got %q", err.Error())
	}
}

// TestStart_BadDriver verifies open failure leaves the service uninitialised.
func TestStart_BadDriver(t *testing.T) {
	s := New(&Config{Driver: "no-such-driver", DSN: ""})
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown driver, got nil")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected DB() to panic after failed Start")
		}
	}()
	_ = s.DB()
}

// TestStart_BadDSN verifies ping failure cleans up the pool.
func TestStart_BadDSN(t *testing.T) {
	// A path that cannot be created: directory doesn't exist.
	s := New(&Config{Driver: "sqlite3", DSN: "/nonexistent-dir/that/cannot/be/created/db.sqlite"})
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected ping/open error, got nil")
	}

	// Stop must be safe and a no-op on the uninitialised service.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after failed Start should be no-op, got %v", err)
	}
}

// TestStop_ClosesPool verifies Stop closes the pool and does not panic when
// called twice.
func TestStop_ClosesPool(t *testing.T) {
	s := newTestService(t)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Second stop: Close on a closed pool returns an error but is harmless; we
	// only require it not panic.
	_ = s.Stop(context.Background())
}

// TestService_SatisfiesInterfaces asserts *Service implements Lifecycle and Provider.
func TestService_SatisfiesInterfaces(t *testing.T) {
	var (
		_ app.Lifecycle = (*Service)(nil)
		_ Provider      = (*Service)(nil)
	)
}
