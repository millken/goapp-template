package db

import (
	"context"
	"io/fs"
	"testing"
)

// newMigratedService opens a shared in-memory database and runs every migration.
// MaxOpenConns=1 so migrations and queries hit the same :memory: connection —
// otherwise each gets a fresh, empty database. Same reason login_test.go does it.
func newMigratedService(t *testing.T, dsn string) *Service {
	t.Helper()
	s := New(&Config{
		Driver:       "sqlite3",
		DSN:          dsn,
		MaxOpenConns: 1,
		Migrations:   &Migrations{},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func TestMigration003_SeedsAdministratorsAndAddsGroupID(t *testing.T) {
	s := newMigratedService(t, ":memory:")
	ctx := context.Background()

	var name string
	var superuser int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT name, superuser FROM user_groups WHERE name = 'Administrators'`).
		Scan(&name, &superuser); err != nil {
		t.Fatalf("Administrators not seeded: %v", err)
	}
	if superuser != 1 {
		t.Errorf("Administrators.superuser = %d, want 1", superuser)
	}

	// The column must exist even with no rows, so select it rather than a row.
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err != nil {
		t.Errorf("users.group_id missing: %v", err)
	}
}

// ALTER TABLE ADD COLUMN is not idempotent — a second run fails with "duplicate
// column name". This pins the property the design actually relies on: the
// migrator records a version and applies each file once. A regression in that
// tracking surfaces here rather than in production.
func TestMigration003_AppliedOnlyOnce(t *testing.T) {
	const dsn = "file:mig003?mode=memory&cache=shared"
	_ = newMigratedService(t, dsn)

	// A second Service over the same database re-runs MigrateUp.
	s2 := New(&Config{
		Driver:       "sqlite3",
		DSN:          dsn,
		MaxOpenConns: 1,
		Migrations:   &Migrations{},
	})
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("second Start re-applied a migration: %v", err)
	}
	_ = s2.Stop(context.Background())
}

// §6.1's claim: DROP COLUMN works on a column declared with REFERENCES. SQLite
// only gained DROP COLUMN in 3.35 and refuses indexed, unique or primary-key
// columns, so this is the part worth testing rather than assuming.
//
// The migrator's version string is the whole filename prefix before ".up.sql"
// (see migrationUpMatcher in dnsoa/go/sqldb's migration.go), so "002_users" is
// what's recorded and compared — not "002" as the brief's placeholder had it.
func TestMigration003_RollsBackCleanly(t *testing.T) {
	s := newMigratedService(t, "file:mig003down?mode=memory&cache=shared")
	ctx := context.Background()

	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub FS: %v", err)
	}
	if err := s.DB().MigrateTo(ctx, sub, "002_users"); err != nil {
		t.Fatalf("migrate down to 002_users: %v", err)
	}

	// Both the table and the column must be gone.
	if _, err := s.DB().ExecContext(ctx, `SELECT 1 FROM user_groups WHERE 1 = 0`); err == nil {
		t.Error("user_groups survived the down migration")
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err == nil {
		t.Error("users.group_id survived the down migration")
	}

	// And up again, so the pair is reversible rather than one-way.
	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err != nil {
		t.Errorf("users.group_id missing after re-apply: %v", err)
	}
}
