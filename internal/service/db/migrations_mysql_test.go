package db

import (
	"context"
	"os"
	"testing"

	"github.com/dnsoa/go/sqldb"
	gosqlmysql "github.com/go-sql-driver/mysql"
)

// requireTestDatabase refuses to run against a database whose name does not
// end in _test. Both round-trip tests MigrateDown the target to zero — run
// against a dev database they drop admins, admin_groups, admin_login_attempts
// and the queue tables, report PASS, and the first human sign of it is
// "no such table" in a running app. The name check is deliberately on the DSN
// (what the test will actually touch), not on some CI variable.
func requireTestDatabase(t *testing.T, dsn string) {
	t.Helper()
	cfg, err := gosqlmysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("MYSQL_TEST_DSN is not a MySQL DSN: %v", err)
	}
	if len(cfg.DBName) < 5 || cfg.DBName[len(cfg.DBName)-5:] != "_test" {
		t.Fatalf("refusing to run: database %q does not end in _test "+
			"(these tests MigrateDown to zero and would drop every table in it)", cfg.DBName)
	}
}

// The SQLite suite (migrations_003_test.go and friends) proves the schema's
// behaviour; it cannot prove the MySQL dialect of the same files parses and
// applies on MySQL itself — a dialect-only syntax error (say, an index outside
// CREATE TABLE) would pass SQLite and fail the first production deploy. That is
// the exact trap migrations/README.md's parity test warns about at the filename
// level; this test closes it at the execution level.
//
// Gated on MYSQL_TEST_DSN rather than a build tag so the file always compiles:
// locally, `docker run -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=app_test
// -p 3306:3306 mysql:8.0` then export the same variable CI uses. CI runs it in
// the migrations job (see .github/workflows/ci.yml).
func TestMySQLMigrations_RoundTripUpDownUp(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set; MySQL round-trip runs in CI's migrations job")
	}
	requireTestDatabase(t, dsn)
	ctx := context.Background()

	// Start runs MigrateUp for the default service — that is the "up" leg, the
	// same code path `serve` takes against production MySQL.
	s := New(&Config{
		Driver:     "mysql",
		DSN:        dsn,
		Migrations: &Migrations{Table: "schema_migrations"},
	})
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start (up): %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(ctx) })
	db := s.DB()

	// The newest migration's column, not just the first table: this is what
	// makes a partially-applied history fail loudly instead of silently.
	if _, err := db.ExecContext(ctx, `SELECT avatar FROM admins WHERE 1 = 0`); err != nil {
		t.Fatalf("up leg incomplete (admins.avatar missing): %v", err)
	}
	var version string
	if err := db.QueryRowContext(ctx,
		`SELECT version FROM schema_migrations WHERE service = 'default'`).
		Scan(&version); err != nil {
		t.Fatalf("read applied version: %v", err)
	}

	sub, err := migrationsFor(sqldb.MySQL)
	if err != nil {
		t.Fatalf("migrationsFor(MySQL): %v", err)
	}

	// Down to zero, then assert the tables are really gone — a down file that
	// no-ops would otherwise surface only when someone needs to roll back for
	// real, which is the worst possible moment to learn it.
	// The table option matters twice over: without it the migrator defaults to
	// the "migrations" table (a different history!), and its own CREATE TABLE
	// DDL is the MySQL-incompatible one the pre-create works around.
	downOpts := []sqldb.MigrationOption{sqldb.WithMigrationTable("schema_migrations")}
	if err := db.MigrateDown(ctx, sub, downOpts...); err != nil {
		t.Fatalf("down to zero: %v", err)
	}
	for _, table := range []string{"admins", "admin_groups", "admin_login_attempts", "app_meta"} {
		if _, err := db.ExecContext(ctx, "SELECT 1 FROM "+table+" WHERE 1 = 0"); err == nil {
			t.Errorf("%s survived the down migration", table)
		}
	}

	// And up again: reversibility is a property of the pair, not of either half.
	if err := db.MigrateUp(ctx, sub, downOpts...); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT avatar FROM admins WHERE 1 = 0`); err != nil {
		t.Fatalf("re-applied schema missing admins.avatar: %v", err)
	}
}
