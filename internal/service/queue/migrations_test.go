package queue

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

// The trap the three-directory layout invites: a migration written for one
// dialect and forgotten in another. A project passes its tests on SQLite and
// fails the first time someone repoints the DSN. internal/service/db has its own
// copy of this check, and it cannot see this directory — so the queue needs one
// too, or carrying its own schema loses the parity guarantee that made the
// three-directory layout safe.
func TestMigrationDirs_HaveIdenticalFilenames(t *testing.T) {
	var reference []string
	var referenceDir string

	for _, flavor := range []sqldb.Flavor{sqldb.SQLite, sqldb.MySQL, sqldb.PostgreSQL} {
		sub, err := migrationsFor(flavor)
		if err != nil {
			t.Fatalf("migrationsFor(%s): %v", flavor, err)
		}
		entries, err := fs.ReadDir(sub, ".")
		if err != nil {
			t.Fatalf("read %s migrations: %v", flavor, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)

		if len(names) == 0 {
			t.Fatalf("%s has no migrations", flavor)
		}
		if reference == nil {
			reference, referenceDir = names, flavor.String()
			continue
		}
		if !slices.Equal(names, reference) {
			t.Errorf("%s migrations differ from %s:\n %s: %v\n %s: %v",
				flavor, referenceDir, flavor, names, referenceDir, reference)
		}
	}

	// Every up needs its down: MigrateTo walks the down files, and a missing one
	// silently stops the rollback at the wrong version rather than failing.
	for _, name := range reference {
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		down := strings.TrimSuffix(name, ".up.sql") + ".down.sql"
		if !slices.Contains(reference, down) {
			t.Errorf("%s has no matching %s", name, down)
		}
	}
}

func TestMigrationsFor_UnknownFlavor(t *testing.T) {
	if _, err := migrationsFor(sqldb.Flavor(0)); err == nil {
		t.Fatal("expected an error for an unknown flavor")
	}
}

// The three tables and their indexes exist after a fresh migrate, and every
// column the queries name is really there. A typo in one dialect's DDL is
// otherwise found by the first query that runs against it.
func TestMigrate_CreatesTheSchema(t *testing.T) {
	db := openMigrated(t, memDSN("mig-schema"))
	ctx := context.Background()

	// SELECT against every column rather than reading sqlite_master: this asserts
	// the names the store's queries actually use, so a column renamed in the DDL
	// fails here instead of at the first claim.
	for _, q := range []string{
		`SELECT id, kind, payload, status, priority, run_at, attempts, max_attempts,
		        timeout_ms, lease_until, lease_token, worker, schedule_id, unique_key,
		        last_error, created_at, updated_at, started_at, finished_at
		   FROM queue_tasks WHERE 1 = 0`,
		`SELECT id, task_id, attempt, outcome, worker, error, started_at, finished_at
		   FROM queue_attempts WHERE 1 = 0`,
		`SELECT id, name, kind, payload, spec, code_spec, enabled, present,
		        next_run_at, last_fire_at, last_task_id, max_attempts, created_at, updated_at
		   FROM queue_schedules WHERE 1 = 0`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Errorf("column check failed: %v\nquery: %s", err, q)
		}
	}

	wantIndexes := []string{
		"queue_tasks_claim", "queue_tasks_lease", "queue_tasks_finished",
		"queue_tasks_schedule", "queue_tasks_unique_key", "queue_attempts_task",
		"queue_schedules_due",
	}
	for _, name := range wantIndexes {
		var got string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&got)
		if err != nil {
			t.Errorf("index %s missing: %v", name, err)
		}
	}
}

// No plan rows are seeded. A cron plan can only come from a code registration, so
// a migration that seeded one would be a plan nothing in the code owns — the
// exact thing "the admin area cannot invent a task with no handler" forbids.
func TestMigrate_SeedsNoSchedules(t *testing.T) {
	db := openMigrated(t, memDSN("mig-seed"))

	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM queue_schedules`).Scan(&n); err != nil {
		t.Fatalf("count schedules: %v", err)
	}
	if n != 0 {
		t.Errorf("migration seeded %d schedule(s), want 0", n)
	}
}

// unique_key is nullable and its unique index must tolerate many NULLs on all
// three dialects — that is what makes "no key means no deduplication" work
// instead of letting the second un-keyed task collide with the first.
func TestMigrate_UniqueKeyToleratesManyNulls(t *testing.T) {
	db := openMigrated(t, memDSN("mig-uniq"))
	ctx := context.Background()

	for range 3 {
		if err := insertBareTask(ctx, db, nil); err != nil {
			t.Fatalf("insert with NULL unique_key: %v", err)
		}
	}
	key := "k"
	if err := insertBareTask(ctx, db, &key); err != nil {
		t.Fatalf("insert with a key: %v", err)
	}
	if err := insertBareTask(ctx, db, &key); err == nil {
		t.Error("a duplicate unique_key was accepted; the unique index is missing")
	}
}

func TestMigrate_RollsBackCleanly(t *testing.T) {
	db := openMigrated(t, memDSN("mig-down"))
	ctx := context.Background()

	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	// Both options, every time. Passing only WithMigrationService reads the
	// version out of sqldb's DEFAULT table, finds no row, and treats every file
	// as not-yet-applied — which makes MigrateDown a silent no-op rather than an
	// error. That is the failure this test caught while being written.
	if err := db.MigrateDown(ctx, sub, testMigrationOpts()...); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	for _, table := range []string{"queue_tasks", "queue_attempts", "queue_schedules"} {
		if _, err := db.ExecContext(ctx, `SELECT 1 FROM `+table+` WHERE 1 = 0`); err == nil {
			t.Errorf("%s survived the down migration", table)
		}
	}
	if err := db.MigrateUp(ctx, sub, testMigrationOpts()...); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
}

// The point of the separate migration service, stated as a test: the queue's
// recorded version is its own row, so it is unaffected by how far the
// application's schema has moved. Without this, a project that skipped the queue
// component and then moved its own mark past the queue's number could never
// apply it — sqldb keeps one high-water mark per service and skips anything
// <= it.
func TestMigrate_VersionIsIsolatedFromTheAppSchema(t *testing.T) {
	db := openMigrated(t, memDSN("mig-isolated"))
	ctx := context.Background()

	// Stand in for an application schema that has run far ahead of 001.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+testMigrationTable+` (service, version) VALUES ('default', '999_far_ahead')`); err != nil {
		t.Fatalf("seed the app service row: %v", err)
	}

	var version, service string
	if err := db.QueryRowContext(ctx,
		`SELECT service, version FROM `+testMigrationTable+` WHERE service = ?`,
		migrationService).Scan(&service, &version); err != nil {
		t.Fatalf("read the queue service row: %v", err)
	}
	if version != "001_queue" {
		t.Errorf("queue version = %q, want %q", version, "001_queue")
	}

	// And a re-run is a no-op rather than a re-apply, which is what the
	// CREATE TABLE IF NOT EXISTS statements would otherwise hide.
	sub, _ := migrationsFor(sqldb.SQLite)
	if err := db.MigrateUp(ctx, sub, testMigrationOpts()...); err != nil {
		t.Fatalf("second MigrateUp: %v", err)
	}
}

// insertBareTask writes a minimally valid task row. It binds every column
// explicitly because no TEXT column in this schema carries a DEFAULT — which is
// itself the thing keeping the MySQL twin free of a length ceiling.
func insertBareTask(ctx context.Context, db *sqldb.DB, uniqueKey *string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO queue_tasks
		   (kind, payload, status, priority, run_at, attempts, max_attempts, timeout_ms,
		    lease_until, lease_token, worker, schedule_id, unique_key, last_error,
		    created_at, updated_at, started_at, finished_at)
		 VALUES ('t', '{}', 'pending', 0, 0, 0, 3, 0, 0, '', '', NULL, ?, '', 0, 0, 0, 0)`,
		uniqueKey)
	return err
}
