package db

import (
	"context"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/app"

	// Register the SQLite driver for tests.
	_ "github.com/millken/goapp-template/internal/driver"
)

// newTestModule builds a Module against an in-memory SQLite database with
// migrations enabled. Each :memory: DB is isolated, so tests don't share state.
func newTestModule(t *testing.T) *Module {
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

// TestDB_BeforeBootPanics verifies DB() panics with a clear message before
// Boot, rather than nil-dereferencing inside a handler.
func TestDB_BeforeBootPanics(t *testing.T) {
	m := newTestModule(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic before Boot")
		}
	}()
	_ = m.DB()
}

// TestBoot_OpensAndMigrates verifies Boot opens the pool, pings it, runs the
// embedded sample migration (creating app_meta), and that DB() then returns a
// usable handle with the migration version recorded.
func TestBoot_OpensAndMigrates(t *testing.T) {
	m := newTestModule(t)
	ctx := context.Background()

	if err := m.Boot(ctx); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(ctx) })

	// The sample migration creates app_meta; verify it exists and is writable.
	db := m.DB()
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

// TestBoot_NilConfig verifies the enable-consistency rule (§4.4): a Use'd module
// with a missing config section fails loudly.
func TestBoot_NilConfig(t *testing.T) {
	m := New(nil)
	err := m.Boot(context.Background())
	if err == nil {
		t.Fatal("expected error for nil config, got nil")
	}
	if !strings.Contains(err.Error(), "config section missing") {
		t.Fatalf("expected 'config section missing' error, got %q", err.Error())
	}
}

// TestBoot_BadDriver verifies open failures are reported and the module is left
// uninitialised (DB() still panics, Shutdown is a no-op).
func TestBoot_BadDriver(t *testing.T) {
	m := New(&Config{Driver: "no-such-driver", DSN: ""})
	err := m.Boot(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown driver, got nil")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected DB() to panic after failed Boot")
		}
	}()
	_ = m.DB()
}

// TestBoot_BadDSN verifies ping failure cleans up the pool (no handle leaked)
// and leaves the module uninitialised.
func TestBoot_BadDSN(t *testing.T) {
	// A file path that cannot be created: directory doesn't exist.
	m := New(&Config{Driver: "sqlite3", DSN: "/nonexistent-dir/that/cannot/be/created/db.sqlite"})
	err := m.Boot(context.Background())
	if err == nil {
		t.Fatal("expected ping/open error, got nil")
	}

	// Shutdown must be safe and a no-op on the uninitialised module.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after failed Boot should be no-op, got %v", err)
	}
}

// TestShutdown_ClosesPool verifies Shutdown closes the pool and is idempotent
// enough not to panic (sql.DB.Close is safe to call repeatedly).
func TestShutdown_ClosesPool(t *testing.T) {
	m := newTestModule(t)
	if err := m.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Second shutdown: Close on a closed pool returns an error but is harmless;
	// we only require it not panic.
	_ = m.Shutdown(context.Background())
}

// TestModule_SatisfiesInterfaces asserts the concrete *Module implements the
// app lifecycle hooks and the Provider contract.
func TestModule_SatisfiesInterfaces(t *testing.T) {
	var (
		_ app.Module     = (*Module)(nil)
		_ app.Booter     = (*Module)(nil)
		_ app.Shutdowner = (*Module)(nil)
		_ Provider       = (*Module)(nil)
	)
}
