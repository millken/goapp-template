package queue

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// readSchedule fetches a plan by name for assertions.
func readSchedule(t *testing.T, db *sqldb.DB, name string) scheduleRow {
	t.Helper()
	r, err := findScheduleByName(context.Background(), db, name)
	if err != nil {
		t.Fatalf("findScheduleByName(%q): %v", name, err)
	}
	return r
}

// --- three-way merge ---

func TestSyncSchedules_InsertsANewPlan(t *testing.T) {
	db := openMigrated(t, memDSN("sync-new"))
	reg := NewRegistry()
	reg.Handle("report", noop)
	reg.Cron("report:nightly", "0 3 * * *", "report", map[string]int{"days": 1},
		WithMaxAttempts(2))

	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(context.Background()); err != nil {
		t.Fatalf("syncSchedules: %v", err)
	}

	row := readSchedule(t, db, "report:nightly")
	if row.Kind != "report" || row.Spec != "0 3 * * *" || row.CodeSpec != "0 3 * * *" {
		t.Errorf("row = %+v", row)
	}
	// spec == code_spec on a fresh row, so it is not drifted and will follow the code.
	if row.Drifted() {
		t.Error("a freshly inserted plan must not read as drifted")
	}
	if !row.Enabled || !row.Present {
		t.Errorf("a new plan should be enabled and present: %+v", row)
	}
	if row.MaxAttempts != 2 {
		t.Errorf("max_attempts = %d, want the registered 2", row.MaxAttempts)
	}
	if row.Payload != `{"days":1}` {
		t.Errorf("payload = %s", row.Payload)
	}
	// next_run_at is the first firing after now, in the service's location.
	want, err := ParseCron("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	next, err := want.Next(clock.now().In(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if row.NextRunAt != next.UnixNano() {
		t.Errorf("next_run_at = %d, want %d", row.NextRunAt, next.UnixNano())
	}
}

// Untouched by an operator and changed in the code: the stored expression follows,
// and next_run_at is recomputed because the old one belonged to the old expression.
func TestSyncSchedules_FollowsTheCodeWhenUntouched(t *testing.T) {
	db := openMigrated(t, memDSN("sync-follows"))

	reg1 := NewRegistry()
	reg1.Handle("report", noop)
	reg1.Cron("nightly", "0 3 * * *", "report", nil)
	s1, _ := newTestService(t, db, reg1, nil)
	if err := s1.syncSchedules(context.Background()); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	before := readSchedule(t, db, "nightly")

	// A later deployment changes the expression in the code.
	reg2 := NewRegistry()
	reg2.Handle("report", noop)
	reg2.Cron("nightly", "*/30 * * * *", "report", nil)
	s2, _ := newTestService(t, db, reg2, nil)
	if err := s2.syncSchedules(context.Background()); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	row := readSchedule(t, db, "nightly")
	if row.Spec != "*/30 * * * *" || row.CodeSpec != "*/30 * * * *" {
		t.Errorf("row = %+v, want both spec and code_spec to follow the code", row)
	}
	if row.Drifted() {
		t.Error("following the code must not leave the row looking drifted")
	}
	if row.NextRunAt == before.NextRunAt {
		t.Error("next_run_at was not recomputed for the new expression")
	}
}

// The requirement, stated as a test: an operator's edit survives a deployment, and
// only the record of what the code says moves forward.
func TestSyncSchedules_KeepsAnOperatorsEdit(t *testing.T) {
	db := openMigrated(t, memDSN("sync-keeps"))
	ctx := context.Background()

	reg1 := NewRegistry()
	reg1.Handle("report", noop)
	reg1.Cron("nightly", "0 3 * * *", "report", nil)
	s1, _ := newTestService(t, db, reg1, nil)
	if err := s1.syncSchedules(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The operator changes it from the admin area: spec moves, code_spec does not.
	id := readSchedule(t, db, "nightly").ID
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET spec = ?, next_run_at = ? WHERE id = ?`,
		"15 4 * * *", int64(12345), id); err != nil {
		t.Fatalf("simulate an operator edit: %v", err)
	}
	if !readSchedule(t, db, "nightly").Drifted() {
		t.Fatal("the edit should read as drifted")
	}

	// A later deployment changes the code's default too.
	reg2 := NewRegistry()
	reg2.Handle("report", noop)
	reg2.Cron("nightly", "0 5 * * *", "report", nil)
	s2, _ := newTestService(t, db, reg2, nil)
	if err := s2.syncSchedules(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	row := readSchedule(t, db, "nightly")
	if row.Spec != "15 4 * * *" {
		t.Errorf("spec = %q, want the operator's 15 4 * * * to survive", row.Spec)
	}
	if row.CodeSpec != "0 5 * * *" {
		t.Errorf("code_spec = %q, want the new code default", row.CodeSpec)
	}
	if !row.Drifted() {
		t.Error("the row should still read as drifted, so the screen can offer a reset")
	}
	// The operator's own next firing is left alone: recomputing it would silently move
	// a schedule somebody set deliberately.
	if row.NextRunAt != 12345 {
		t.Errorf("next_run_at = %d, want the operator's 12345 untouched", row.NextRunAt)
	}
}

// enabled belongs to the operator and sync never writes it. Without this, a paused
// plan would resume itself on every restart.
func TestSyncSchedules_NeverReEnablesAPausedPlan(t *testing.T) {
	db := openMigrated(t, memDSN("sync-paused"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("report", noop)
	reg.Cron("nightly", "0 3 * * *", "report", nil)
	s, _ := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	id := readSchedule(t, db, "nightly").ID
	if _, err := db.ExecContext(ctx, `UPDATE queue_schedules SET enabled = 0 WHERE id = ?`, id); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if readSchedule(t, db, "nightly").Enabled {
		t.Error("sync re-enabled a plan an operator had paused")
	}
}

// A plan the code dropped is flagged, not deleted: deleting would throw away the
// operator's edit and orphan the schedule_id of every task it produced.
func TestSyncSchedules_FlagsAPlanTheCodeDropped(t *testing.T) {
	db := openMigrated(t, memDSN("sync-absent"))
	ctx := context.Background()

	reg1 := NewRegistry()
	reg1.Handle("a", noop)
	reg1.Handle("b", noop)
	reg1.Cron("plan-a", "@daily", "a", nil)
	reg1.Cron("plan-b", "@daily", "b", nil)
	s1, _ := newTestService(t, db, reg1, nil)
	if err := s1.syncSchedules(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The next deployment no longer declares plan-b.
	reg2 := NewRegistry()
	reg2.Handle("a", noop)
	reg2.Cron("plan-a", "@daily", "a", nil)
	s2, _ := newTestService(t, db, reg2, nil)
	if err := s2.syncSchedules(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_schedules`); n != 2 {
		t.Errorf("%d plans, want 2: a dropped plan is flagged, not deleted", n)
	}
	if !readSchedule(t, db, "plan-a").Present {
		t.Error("plan-a should still be present")
	}
	if readSchedule(t, db, "plan-b").Present {
		t.Error("plan-b should be flagged present=0")
	}

	// And it must not fire.
	clock := newFakeClock()
	s2.now = clock.now
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET next_run_at = 1 WHERE name = 'plan-b'`); err != nil {
		t.Fatal(err)
	}
	if err := s2.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 0 {
		t.Errorf("a flagged plan fired %d task(s)", n)
	}
}

// A plan that comes back is un-flagged, with the operator's edit intact — which is
// the payoff for flagging instead of deleting.
func TestSyncSchedules_RestoresAReturnedPlan(t *testing.T) {
	db := openMigrated(t, memDSN("sync-restore"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Cron("plan-a", "@daily", "a", nil)
	s, _ := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	id := readSchedule(t, db, "plan-a").ID
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET present = 0, spec = ?, enabled = 0 WHERE id = ?`,
		"0 9 * * *", id); err != nil {
		t.Fatal(err)
	}

	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	row := readSchedule(t, db, "plan-a")
	if !row.Present {
		t.Error("a returned plan should be present again")
	}
	if row.Spec != "0 9 * * *" {
		t.Errorf("spec = %q, want the operator's edit to have survived", row.Spec)
	}
	if row.Enabled {
		t.Error("enabled is the operator's column; sync must not have restored it")
	}
}

// Running sync twice must change nothing but updated_at. Otherwise every restart
// nudges next_run_at, and a plan on a frequently-restarting deployment drifts.
func TestSyncSchedules_IsIdempotent(t *testing.T) {
	db := openMigrated(t, memDSN("sync-idempotent"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Cron("plan-a", "0 3 * * *", "a", map[string]int{"n": 1})
	s, clock := newTestService(t, db, reg, nil)

	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	first := readSchedule(t, db, "plan-a")

	clock.advance(time.Hour)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	second := readSchedule(t, db, "plan-a")

	if first != second {
		t.Errorf("sync is not idempotent:\n first  = %+v\n second = %+v", first, second)
	}
}

// A registry that is not complete must not touch anything: it would mark plans it
// cannot see as dropped.
func TestSyncSchedules_SkippedForAnIncompleteRegistry(t *testing.T) {
	db := openMigrated(t, memDSN("sync-incomplete"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Cron("plan-a", "@daily", "a", nil)
	s, _ := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// A hypothetical `--kinds` worker: a subset registry.
	partial := NewRegistry()
	partial.complete = false
	sp, _ := newTestService(t, db, partial, nil)
	if err := sp.syncSchedules(ctx); err != nil {
		t.Fatalf("sync with a partial registry: %v", err)
	}
	if !readSchedule(t, db, "plan-a").Present {
		t.Error("a partial registry marked a plan it cannot see as dropped")
	}
}

// --- firing ---

func TestFireDueSchedules_EnqueuesAndAdvances(t *testing.T) {
	db := openMigrated(t, memDSN("fire"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("report", noop)
	reg.Cron("nightly", "0 3 * * *", "report", map[string]int{"n": 1}, WithMaxAttempts(2))
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	before := readSchedule(t, db, "nightly")
	// Move to the firing instant.
	clock.set(time.Unix(0, before.NextRunAt).In(time.UTC))
	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}

	var (
		id, scheduleID int64
		kind, payload  string
		maxAttempts    int
		uniqueKey      string
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, kind, payload, max_attempts, schedule_id, unique_key FROM queue_tasks`).
		Scan(&id, &kind, &payload, &maxAttempts, &scheduleID, &uniqueKey)
	if err != nil {
		t.Fatalf("read the fired task: %v", err)
	}
	if kind != "report" || payload != `{"n":1}` || maxAttempts != 2 {
		t.Errorf("task = %s/%s/%d", kind, payload, maxAttempts)
	}
	if scheduleID != before.ID {
		t.Errorf("schedule_id = %d, want %d: the plan's history depends on it", scheduleID, before.ID)
	}
	if want := "cron:1:" + strconv.FormatInt(before.NextRunAt, 10); uniqueKey != want {
		t.Errorf("unique_key = %q, want %q", uniqueKey, want)
	}

	after := readSchedule(t, db, "nightly")
	if after.NextRunAt <= before.NextRunAt {
		t.Errorf("next_run_at did not advance: %d -> %d", before.NextRunAt, after.NextRunAt)
	}
	if after.LastTaskID != id {
		t.Errorf("last_task_id = %d, want %d: the admin list shows the plan's last outcome through it",
			after.LastTaskID, id)
	}
	if after.LastFireAt == 0 {
		t.Error("last_fire_at was not recorded")
	}
}

// The arbitration itself, at the level where it can actually be wrong.
//
// Two instances that both read next_run_at BEFORE either writes is the only
// situation the compare-and-swap exists for, and it is the situation a sequential
// test cannot produce: once one instance has fired, next_run_at is in the future and
// the second one's due query returns nothing, so it never reaches the CAS at all.
// Calling claimScheduleFiring directly with the same witness reproduces the
// interleaving exactly.
func TestClaimScheduleFiring_RejectsAStaleWitness(t *testing.T) {
	db := openMigrated(t, memDSN("fire-cas"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Cron("plan", "@daily", "a", nil)
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	row := readSchedule(t, db, "plan")
	witness := row.NextRunAt
	next := witness + int64(24*time.Hour)
	now := clock.now().UnixNano()

	// Instance A claims the firing.
	won, err := claimScheduleFiring(ctx, db, row.ID, witness, next, now)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !won {
		t.Fatal("the first claimant lost its own firing")
	}

	// Instance B read the same witness a moment earlier and only now tries to write.
	won, err = claimScheduleFiring(ctx, db, row.ID, witness, next, now)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if won {
		t.Fatal("a stale witness won the firing; two instances would both enqueue")
	}

	// And next_run_at advanced exactly once.
	if got := readSchedule(t, db, "plan").NextRunAt; got != next {
		t.Errorf("next_run_at = %d, want %d advanced once", got, next)
	}
}

// The same guarantee end to end: four instances, one clock, one firing instant.
//
// This is a smoke test, not the proof — and the difference is worth recording,
// because the obvious reading of it is wrong. SQLite serialises writers so
// thoroughly that the interleaving the compare-and-swap defends against almost never
// occurs here: deleting the witness from claimScheduleFiring leaves this test green.
// TestClaimScheduleFiring_RejectsAStaleWitness is what actually holds the
// arbitration, by reproducing the interleaving directly.
//
// What this one is still worth: it exercises the whole path — due query, parse,
// claim, insert, last_task_id — from several goroutines at once, so a deadlock or a
// shared-state bug between them shows up under -race.
func TestFireDueSchedules_FiresOnceUnderRealConcurrency(t *testing.T) {
	db := openMigratedConcurrent(t, 4)
	ctx := context.Background()

	const instances = 4
	services := make([]*Service, instances)
	for i := range services {
		reg := NewRegistry()
		reg.Handle("report", noop)
		reg.Cron("nightly", "0 3 * * *", "report", nil)
		s, _ := newTestService(t, db, reg, nil)
		services[i] = s
	}
	if err := services[0].syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Every instance sees the same instant, and it is the firing instant.
	due := time.Unix(0, readSchedule(t, db, "nightly").NextRunAt).In(time.UTC)
	shared := newFakeClock()
	shared.set(due)
	for _, s := range services {
		s.now = shared.now
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, s := range services {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			<-start
			if err := s.fireDueSchedules(ctx); err != nil {
				t.Errorf("fireDueSchedules: %v", err)
			}
		}(s)
	}
	close(start)
	wg.Wait()

	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 1 {
		t.Errorf("%d tasks for one firing across %d instances, want 1", n, instances)
	}
}

// A plan whose next firing is long past fires ONCE and then jumps to the future.
// Catch-up would dump one task per missed occurrence into the queue the moment
// service returned, which for a maintenance job is never what is wanted.
func TestFireDueSchedules_DoesNotCatchUp(t *testing.T) {
	db := openMigrated(t, memDSN("fire-nocatchup"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("cleanup", noop)
	reg.Cron("daily", "0 3 * * *", "cleanup", nil)
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Three days of downtime.
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET next_run_at = ? WHERE name = 'daily'`,
		clock.now().Add(-72*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}

	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 1 {
		t.Errorf("%d tasks after three days down, want exactly 1", n)
	}
	if next := readSchedule(t, db, "daily").NextRunAt; next <= clock.now().UnixNano() {
		t.Error("next_run_at is still in the past; the plan would fire again immediately")
	}

	// And a second pass in the same instant fires nothing more.
	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 1 {
		t.Errorf("%d tasks after a second pass, want still 1", n)
	}
}

func TestFireDueSchedules_SkipsPausedAndFuturePlans(t *testing.T) {
	db := openMigrated(t, memDSN("fire-skip"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Cron("paused", "@daily", "a", nil)
	reg.Cron("future", "@daily", "a", nil)
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET enabled = 0, next_run_at = ? WHERE name = 'paused'`,
		clock.now().Add(-time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	// "future" keeps its computed next_run_at, which is ahead of now.

	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 0 {
		t.Errorf("%d tasks fired, want 0 (one paused, one not yet due)", n)
	}
}

// A row edited by hand can carry an expression or a kind the code would have
// refused. One such row must not stop the others from firing.
func TestFireDueSchedules_SurvivesABadRow(t *testing.T) {
	db := openMigrated(t, memDSN("fire-badrow"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("good", noop)
	reg.Cron("good-plan", "@daily", "good", nil)
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	past := clock.now().Add(-time.Hour).UnixNano()
	// Two hand-written rows: one with an unparseable expression, one whose kind has
	// no handler.
	for _, row := range []struct{ name, spec, kind string }{
		{"bad-spec", "not a cron", "good"},
		{"bad-kind", "@daily", "missing"},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO queue_schedules
			   (name, kind, payload, spec, code_spec, enabled, present, next_run_at,
			    last_fire_at, last_task_id, max_attempts, created_at, updated_at)
			 VALUES (?, ?, '{}', ?, ?, 1, 1, ?, 0, 0, 3, 0, 0)`,
			row.name, row.kind, row.spec, row.spec, past); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET next_run_at = ? WHERE name = 'good-plan'`, past); err != nil {
		t.Fatal(err)
	}

	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules should not fail on a bad row: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE kind = 'good'`); n != 1 {
		t.Errorf("the good plan fired %d task(s), want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 1 {
		t.Errorf("%d tasks total, want only the good one", n)
	}
}

// A kind's registered options apply however its tasks are produced.
//
// WithTimeout and WithPriority are documented as properties of the kind, but only
// Enqueue used to read them: both cron producers built their params by hand, so
// every cron-fired task was stored with timeout_ms = 0 and priority = 0 and silently
// fell back to the global task_timeout. The template's own example registers
// example:heartbeat with WithTimeout, which made the option look wired up while
// doing nothing.
func TestScheduleFirings_CarryTheKindsRegisteredOptions(t *testing.T) {
	db := openMigrated(t, memDSN("fire-options"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("report", noop,
		WithMaxAttempts(5), WithTimeout(90*time.Second), WithPriority(7))
	reg.Cron("nightly", "0 3 * * *", "report", nil)
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	row := readSchedule(t, db, "nightly")

	// Both producers: the scheduler, and the admin area's "run it now".
	clock.set(time.Unix(0, row.NextRunAt).In(time.UTC))
	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}
	if _, err := s.TriggerSchedule(ctx, row.ID); err != nil {
		t.Fatalf("TriggerSchedule: %v", err)
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, max_attempts, timeout_ms, priority FROM queue_tasks ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, maxAtt, timeoutMS, priority int
		if err := rows.Scan(&id, &maxAtt, &timeoutMS, &priority); err != nil {
			t.Fatal(err)
		}
		n++
		if maxAtt != 5 || timeoutMS != 90_000 || priority != 7 {
			t.Errorf("task %d = max_attempts %d, timeout_ms %d, priority %d; "+
				"want 5, 90000, 7 from the registration", id, maxAtt, timeoutMS, priority)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d tasks, want one from each producer", n)
	}
}

// --- trigger by hand ---

// A hand-fired plan has to produce the same task the scheduler would have produced.
//
// The one that got away: a plan registered without WithMaxAttempts stores
// max_attempts = 0, and TriggerSchedule passed that through verbatim while
// fireDueSchedules fell back to the service default. The hand-fired task was
// therefore dead on its first failure with no retry — for the plan most likely to be
// fired by hand, since triggering one is how an operator checks a plan that has been
// failing.
func TestTriggerSchedule_MatchesAnAutomaticFiring(t *testing.T) {
	db := openMigrated(t, memDSN("trigger-defaults"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("report", noop)
	// No WithMaxAttempts: the plan stores 0 and both firing paths must supply the
	// service default.
	reg.Cron("nightly", "0 3 * * *", "report", map[string]int{"n": 1})
	s, clock := newTestService(t, db, reg, nil)
	if err := s.syncSchedules(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	row := readSchedule(t, db, "nightly")
	if row.MaxAttempts != 0 {
		t.Fatalf("max_attempts = %d: this test needs a plan that stores 0", row.MaxAttempts)
	}

	taskID, err := s.TriggerSchedule(ctx, row.ID)
	if err != nil {
		t.Fatalf("TriggerSchedule: %v", err)
	}
	if taskID == 0 {
		t.Fatal("TriggerSchedule returned id 0: the admin area reads that as 'the plan is gone'")
	}
	if got := readTask(t, db, taskID).MaxAttempts; got != s.MaxAttempts() {
		t.Errorf("max_attempts = %d, want the service default %d; %d would make the "+
			"first failure terminal", got, s.MaxAttempts(), got)
	}

	// next_run_at is deliberately untouched, so the automatic firing still happens.
	if after := readSchedule(t, db, "nightly"); after.NextRunAt != row.NextRunAt {
		t.Errorf("next_run_at moved: %d -> %d; a manual run must not skip a cycle",
			row.NextRunAt, after.NextRunAt)
	}
	if after := readSchedule(t, db, "nightly"); after.LastTaskID != taskID {
		t.Errorf("last_task_id = %d, want %d", after.LastTaskID, taskID)
	}

	// And the scheduler's own firing agrees with it.
	clock.set(time.Unix(0, row.NextRunAt).In(time.UTC))
	if err := s.fireDueSchedules(ctx); err != nil {
		t.Fatalf("fireDueSchedules: %v", err)
	}
	fired := readSchedule(t, db, "nightly").LastTaskID
	if fired == taskID {
		t.Fatal("the scheduled firing did not produce a second task")
	}
	if got, want := readTask(t, db, fired).MaxAttempts, readTask(t, db, taskID).MaxAttempts; got != want {
		t.Errorf("scheduled firing got max_attempts %d, hand-fired got %d", got, want)
	}
}
