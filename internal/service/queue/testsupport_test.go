package queue

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"

	// The sqlite3 driver, which database/sql needs registered before Open. The
	// application registers all three at its composition root
	// (internal/driver); a package test has no composition root, so it
	// blank-imports the same package.
	_ "github.com/millken/goapp-template/internal/driver"
)

// testMigrationTable is the migrations table these tests point the migrator at.
// Named explicitly rather than left to sqldb's default so the isolation test can
// read rows out of it by name.
const testMigrationTable = "schema_migrations"

// testMigrationOpts is the option pair every migrator call in this package's
// tests needs. Both, always: passing only WithMigrationService reads the version
// from sqldb's default table, finds no row for this service, and so treats every
// file as unapplied — which turns MigrateDown into a silent no-op instead of an
// error.
func testMigrationOpts() []sqldb.MigrationOption {
	return []sqldb.MigrationOption{
		sqldb.WithMigrationService(migrationService),
		sqldb.WithMigrationTable(testMigrationTable),
	}
}

// memDSN returns a shared-cache in-memory DSN. Shared cache is what lets more
// than one connection — and a second Open of the same name — see the same
// database, which the migration tests need; a plain ":memory:" gives every
// connection its own empty one.
//
// Each caller passes a distinct name so tests do not collide when run in
// parallel or reordered.
func memDSN(name string) string {
	return "file:queue-" + name + "?mode=memory&cache=shared"
}

// openMigrated opens a SQLite handle with the queue schema applied and closes it
// on cleanup.
//
// MaxOpenConns(1) matches the rest of the suite: the queue's read and write paths
// deliberately avoid transactions (the atomicity comes from the CAS itself), so
// the deadlock this setting invites — documented on Admin.keepingASuperuser —
// has nothing here to bite. The one test that needs real concurrency uses a file
// database instead, and says so.
func openMigrated(t *testing.T, dsn string) *sqldb.DB {
	t.Helper()

	db, err := sqldb.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping %s: %v", dsn, err)
	}
	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	if err := db.MigrateUp(context.Background(), sub, testMigrationOpts()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// openMigratedConcurrent opens a FILE-backed SQLite database with several
// connections, for the one thing an in-memory handle with MaxOpenConns(1) cannot
// test: genuine concurrent claiming.
//
// This is the only test in the repository that does not use ":memory:", and the
// reason is specific. With one connection the pool serialises every statement, so
// a claim test against it proves the bookkeeping discipline — that RowsAffected
// == 0 is treated as "someone else won" and not as an error — but proves nothing
// about the engine's locking. Only real connections contending for real row locks
// do that, and the compare-and-swap in claimTasks is the piece of this package
// most expensive to get wrong.
//
// It stays hermetic: a file under t.TempDir(), no external service.
//
// The DSN parameters are the ones a SQLite deployment of the queue needs, which is
// the second thing this test documents. WAL lets readers run during a write;
// _busy_timeout makes a blocked writer wait instead of returning SQLITE_BUSY
// immediately; _txlock=immediate takes the write lock up front rather than
// upgrading mid-statement. Without them a multi-worker SQLite queue fails with
// "database is locked" under any real load — see the note in config.example.yaml.
func openMigratedConcurrent(t *testing.T, conns int) *sqldb.DB {
	t.Helper()

	dsn := "file:" + filepath.Join(t.TempDir(), "queue.db") +
		"?_journal=WAL&_busy_timeout=5000&_txlock=immediate"

	db, err := sqldb.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(conns)

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping %s: %v", dsn, err)
	}
	sub, err := migrationsFor(sqldb.SQLite)
	if err != nil {
		t.Fatalf("migrationsFor: %v", err)
	}
	if err := db.MigrateUp(context.Background(), sub, testMigrationOpts()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seed is a task row to insert, with only the fields a test cares about set.
// Everything else takes a value that makes the row uninteresting to the code under
// test.
type seed struct {
	Kind        string
	Status      string
	Payload     string
	RunAt       int64
	Priority    int
	Attempts    int
	MaxAttempts int
	LeaseUntil  int64
	LeaseToken  string
	Worker      string
	ScheduleID  int64
	CreatedAt   int64
	FinishedAt  int64
}

// seedTask inserts a task in an arbitrary state and returns its id. Tests use it
// to construct the situations only a crash or a race would otherwise produce — a
// running row whose lease has already expired, for instance.
func seedTask(t *testing.T, db *sqldb.DB, s seed) int64 {
	t.Helper()

	if s.Kind == "" {
		s.Kind = "test:kind"
	}
	if s.Status == "" {
		s.Status = StatusPending
	}
	if s.Payload == "" {
		s.Payload = "{}"
	}
	if s.MaxAttempts == 0 {
		s.MaxAttempts = 3
	}

	res, err := db.ExecContext(context.Background(),
		`INSERT INTO queue_tasks
		   (kind, payload, status, priority, run_at, attempts, max_attempts, timeout_ms,
		    lease_until, lease_token, worker, schedule_id, unique_key, last_error,
		    created_at, updated_at, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, NULL, '', ?, ?, 0, ?)`,
		s.Kind, s.Payload, s.Status, s.Priority, s.RunAt, s.Attempts, s.MaxAttempts,
		s.LeaseUntil, s.LeaseToken, s.Worker, nullableID(s.ScheduleID),
		s.CreatedAt, s.CreatedAt, s.FinishedAt)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed task id: %v", err)
	}
	return id
}

// taskState is the subset of a row the assertions read back.
type taskState struct {
	Status      string
	RunAt       int64
	Attempts    int
	MaxAttempts int
	LeaseUntil  int64
	LeaseToken  string
	Worker      string
	LastError   string
	FinishedAt  int64
}

func readTask(t *testing.T, db *sqldb.DB, id int64) taskState {
	t.Helper()
	var ts taskState
	err := db.QueryRowContext(context.Background(),
		`SELECT status, run_at, attempts, max_attempts, lease_until, lease_token,
		        worker, last_error, finished_at
		   FROM queue_tasks WHERE id = ?`, id).
		Scan(&ts.Status, &ts.RunAt, &ts.Attempts, &ts.MaxAttempts, &ts.LeaseUntil,
			&ts.LeaseToken, &ts.Worker, &ts.LastError, &ts.FinishedAt)
	if err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return ts
}

// fakeClock is the injected clock. Every periodic job is a named method taking a
// context, so a test drives time by moving this rather than by sleeping — nothing in
// this package's tests waits for a duration it configured.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	// A fixed instant in UTC. UTC so a developer's TZ cannot change what a cron
	// assertion means; fixed so a failure is reproducible.
	return &fakeClock{t: time.Date(2026, 8, 12, 10, 7, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// discardLogger keeps the expected warnings of reaper and orphan tests out of the
// test output. A test that cares about a log line asserts on behaviour instead —
// there is nothing here that is only observable as a log message.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestService builds a service that has NOT been Started: cfg is filled in, the
// clock and jitter are deterministic, and the fields Start would otherwise set are
// set here.
//
// Tests of the periodic jobs use this and call the methods directly. Tests of
// Start/Stop call Start on the result, which is safe because Start overwrites
// workerID and loc with the values it derives.
func newTestService(t *testing.T, db *sqldb.DB, reg *Registry, cfg *Config) (*Service, *fakeClock) {
	t.Helper()

	if reg == nil {
		reg = NewRegistry()
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Concurrency == nil {
		cfg.Concurrency = ptr(0)
	}

	clock := newFakeClock()
	s := New(cfg, db, reg, discardLogger())
	s.now = clock.now
	// Zero jitter: every backoff assertion then lands on the floor of the equal-jitter
	// band, which is an exact number instead of a range.
	s.jitter = func(int64) int64 { return 0 }
	s.loc = time.UTC
	s.workerID = "test-worker"
	return s, clock
}

func ptr[T any](v T) *T { return &v }

func countRows(t *testing.T, db *sqldb.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
