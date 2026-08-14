package db

import (
	"context"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

// A constant DEFAULT on ADD COLUMN fills existing rows, so unlike 003 this
// migration needs no backfill — but that is a claim about SQLite's behaviour,
// and it is the difference between an upgraded project working and every one of
// its users being locked out.
func TestMigration004_ExistingUsersLandActive(t *testing.T) {
	const dsn = "file:mig004?mode=memory&cache=shared"
	ctx := context.Background()

	s := New(&Config{Driver: "sqlite3", DSN: dsn, MaxOpenConns: 1, Migrations: &Migrations{}})
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(ctx) })

	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	// Rewind to the state a project on 003 is in, then add a user the way it
	// would have been created before status existed.
	if err := s.DB().MigrateTo(ctx, sub, "003_admin_groups"); err != nil {
		t.Fatalf("migrate down to 003_admin_groups: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO admins (username, password_hash, created_at, group_id)
		 VALUES ('legacy', 'x', 0, (SELECT id FROM admin_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatalf("seed pre-existing user: %v", err)
	}

	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("upgrade to 004: %v", err)
	}

	var status int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT status FROM admins WHERE username = 'legacy'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != 1 {
		t.Errorf("pre-existing user landed with status = %d, want 1 (locked out)", status)
	}
}

// ALTER TABLE ADD COLUMN is not idempotent — a second run fails with "duplicate
// column name". This pins what the design relies on: the migrator records a
// version and applies each file once.
func TestMigration004_AppliedOnlyOnce(t *testing.T) {
	const dsn = "file:mig004once?mode=memory&cache=shared"
	_ = newMigratedService(t, dsn)

	s2 := New(&Config{Driver: "sqlite3", DSN: dsn, MaxOpenConns: 1, Migrations: &Migrations{}})
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("second Start re-applied a migration: %v", err)
	}
	_ = s2.Stop(context.Background())
}

func TestMigration004_RollsBackCleanly(t *testing.T) {
	s := newMigratedService(t, "file:mig004down?mode=memory&cache=shared")
	ctx := context.Background()

	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	if err := s.DB().MigrateTo(ctx, sub, "003_admin_groups"); err != nil {
		t.Fatalf("migrate down to 003_admin_groups: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT status FROM admins WHERE 1 = 0`); err == nil {
		t.Error("users.status survived the down migration")
	}
	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
}
