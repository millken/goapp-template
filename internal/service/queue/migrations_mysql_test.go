package queue

import (
	"context"
	"os"
	"testing"

	"github.com/dnsoa/go/sqldb"
	gosqlmysql "github.com/go-sql-driver/mysql"
)

// The queue owns its schema wholesale (see migrations.go), so its MySQL dialect
// needs its own execution-level check — internal/service/db's copy cannot see
// this directory. CI provisions MySQL via a service container and sets
// MYSQL_TEST_DSN; locally, run mysql:8.0 in Docker and export the same variable.
func TestMySQLMigrations_RoundTripUpDownUp(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set; MySQL round-trip runs in CI's migrations job")
	}
	// Same guard as the db package's test: this drops the queue tables, so it
	// only ever runs against a *_test database.
	if cfg, err := gosqlmysql.ParseDSN(dsn); err != nil {
		t.Fatalf("MYSQL_TEST_DSN is not a MySQL DSN: %v", err)
	} else if n := cfg.DBName; len(n) < 5 || n[len(n)-5:] != "_test" {
		t.Fatalf("refusing to run: database %q does not end in _test", n)
	}
	ctx := context.Background()

	db, err := sqldb.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The queue's migrate() runs before any db.Service has started in this
	// test, so the migrations-table workaround for sqldb's MySQL-incompatible
	// DDL (TEXT primary key, Error 1170) has to be applied here — in production
	// db.Service.Start does it before the queue ever migrates.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			service VARCHAR(191) NOT NULL,
			version VARCHAR(191) NOT NULL DEFAULT '',
			PRIMARY KEY (service)
		)`); err != nil {
		t.Fatalf("precreate migrations table: %v", err)
	}

	// The same call queue.Service.Start makes: default service name, the db
	// component's migrations table.
	if err := migrate(ctx, db, "schema_migrations"); err != nil {
		t.Fatalf("migrate (up): %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT unique_key FROM queue_tasks WHERE 1 = 0`); err != nil {
		t.Fatalf("up leg incomplete (queue_tasks.unique_key missing): %v", err)
	}

	sub, err := migrationsFor(sqldb.MySQL)
	if err != nil {
		t.Fatalf("migrationsFor(MySQL): %v", err)
	}

	// Down under the queue's own migration service — the db component's history
	// row (if a sibling test applied it) must be untouched by this leg. The
	// table option is load-bearing for the same reason as in the db package's
	// test: the default "migrations" table is a different history, and the
	// migrator's own CREATE TABLE DDL trips MySQL Error 1170.
	opts := []sqldb.MigrationOption{
		sqldb.WithMigrationService(migrationService),
		sqldb.WithMigrationTable("schema_migrations"),
	}
	if err := db.MigrateDown(ctx, sub, opts...); err != nil {
		t.Fatalf("down to zero: %v", err)
	}
	for _, table := range []string{"queue_attempts", "queue_tasks", "queue_schedules"} {
		if _, err := db.ExecContext(ctx, "SELECT 1 FROM "+table+" WHERE 1 = 0"); err == nil {
			t.Errorf("%s survived the down migration", table)
		}
	}

	if err := db.MigrateUp(ctx, sub, opts...); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT unique_key FROM queue_tasks WHERE 1 = 0`); err != nil {
		t.Fatalf("re-applied schema missing queue_tasks.unique_key: %v", err)
	}
}
