package db

import (
	"context"
	"testing"

	"github.com/dnsoa/go/sqldb"
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
		`SELECT name, superuser FROM admin_groups WHERE name = 'Administrators'`).
		Scan(&name, &superuser); err != nil {
		t.Fatalf("Administrators not seeded: %v", err)
	}
	if superuser != 1 {
		t.Errorf("Administrators.superuser = %d, want 1", superuser)
	}

	// The column must exist even with no rows, so select it rather than a row.
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM admins WHERE 1 = 0`); err != nil {
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
// (see migrationUpMatcher in dnsoa/go/sqldb's migration.go), so "002_admins" is
// what's recorded and compared — not "002" as the brief's placeholder had it.
func TestMigration003_RollsBackCleanly(t *testing.T) {
	s := newMigratedService(t, "file:mig003down?mode=memory&cache=shared")
	ctx := context.Background()

	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	if err := s.DB().MigrateTo(ctx, sub, "002_admins"); err != nil {
		t.Fatalf("migrate down to 002_admins: %v", err)
	}

	// Both the table and the column must be gone.
	if _, err := s.DB().ExecContext(ctx, `SELECT 1 FROM admin_groups WHERE 1 = 0`); err == nil {
		t.Error("admin_groups survived the down migration")
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM admins WHERE 1 = 0`); err == nil {
		t.Error("users.group_id survived the down migration")
	}

	// And up again, so the pair is reversible rather than one-way.
	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM admins WHERE 1 = 0`); err != nil {
		t.Errorf("users.group_id missing after re-apply: %v", err)
	}
}

// An existing project already has 001 and 002 applied and real users in the
// table. group_id is nullable, so without a backfill every one of them resolves
// to errNoGroup and is refused — including on the dashboard and logout, because
// resolve runs before the exemption. They cannot even sign out. Before this
// migration they had no permission checks at all, so Administrators is what
// preserves their access rather than granting new.
func TestMigration003_BackfillsUsersThatPredateIt(t *testing.T) {
	const dsn = "file:mig003backfill?mode=memory&cache=shared"
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
	// Rewind to the state an existing project is in, then add a user the way it
	// would have been created before groups existed.
	if err := s.DB().MigrateTo(ctx, sub, "002_admins"); err != nil {
		t.Fatalf("migrate down to 002_admins: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO admins (username, password_hash, created_at) VALUES ('legacy', 'x', 0)`); err != nil {
		t.Fatalf("seed pre-existing user: %v", err)
	}

	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("upgrade to 003: %v", err)
	}

	var groupName string
	if err := s.DB().QueryRowContext(ctx,
		`SELECT g.name FROM admins u JOIN admin_groups g ON g.id = u.group_id
		 WHERE u.username = 'legacy'`).Scan(&groupName); err != nil {
		t.Fatalf("pre-existing user has no group after upgrade: %v", err)
	}
	if groupName != "Administrators" {
		t.Errorf("legacy user landed in %q, want Administrators", groupName)
	}
}
