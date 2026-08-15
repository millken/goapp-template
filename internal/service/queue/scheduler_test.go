package queue

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- reaper ---

// Crash recovery: a `kill -9` leaves a running row with a lease nobody renews, and
// this is what brings it back.
func TestReapOnce_ReturnsALostTaskToPending(t *testing.T) {
	db := openMigrated(t, memDSN("reap-pending"))
	s, clock := newTestService(t, db, NewRegistry(), &Config{
		Concurrency: ptr(1), RetryBase: 15 * time.Second, RetryMax: time.Hour,
	})

	id := seedTask(t, db, seed{
		Kind: "k", Status: StatusRunning, Attempts: 1, MaxAttempts: 3,
		LeaseUntil: clock.now().Add(-time.Minute).UnixNano(),
		LeaseToken: "gone-token", Worker: "gone-worker",
	})

	if err := s.reapOnce(context.Background()); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}

	row := readTask(t, db, id)
	if row.Status != StatusPending {
		t.Errorf("status = %q, want pending", row.Status)
	}
	// attempts is NOT touched. It was consumed at claim time, and that is exactly what
	// makes a task which kills its process eventually stop rather than retry forever.
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 unchanged: the crash already consumed it", row.Attempts)
	}
	if want := clock.now().Add(7500 * time.Millisecond).UnixNano(); row.RunAt != want {
		t.Errorf("run_at = %d, want %d (backoff applied)", row.RunAt, want)
	}
	if row.LeaseUntil != 0 || row.LeaseToken != "" {
		t.Errorf("lease not cleared: %+v", row)
	}
	if !strings.Contains(row.LastError, "lease expired") {
		t.Errorf("last_error = %q, want it to explain the crash", row.LastError)
	}

	// The crash has to be visible in the failure log, attributed to the worker that
	// vanished rather than to the one that reaped it.
	att := attemptsOf(t, db, id)
	if len(att) != 1 {
		t.Fatalf("attempts = %+v, want one", att)
	}
	if att[0].Outcome != OutcomeLost {
		t.Errorf("outcome = %q, want %q", att[0].Outcome, OutcomeLost)
	}
	if att[0].Worker != "gone-worker" {
		t.Errorf("worker = %q, want the vanished worker", att[0].Worker)
	}
}

func TestReapOnce_GivesUpWhenAttemptsAreSpent(t *testing.T) {
	db := openMigrated(t, memDSN("reap-dead"))
	s, clock := newTestService(t, db, NewRegistry(), nil)

	id := seedTask(t, db, seed{
		Kind: "k", Status: StatusRunning, Attempts: 3, MaxAttempts: 3,
		LeaseUntil: clock.now().Add(-time.Minute).UnixNano(),
		LeaseToken: "tok", Worker: "gone",
	})

	if err := s.reapOnce(context.Background()); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}
	row := readTask(t, db, id)
	if row.Status != StatusDead || row.FinishedAt == 0 {
		t.Errorf("row = %+v, want dead with a finished_at", row)
	}
	if att := attemptsOf(t, db, id); len(att) != 1 || att[0].Outcome != OutcomeLost {
		t.Errorf("attempts = %+v, want one lost attempt", att)
	}
}

func TestReapOnce_LeavesLiveLeasesAlone(t *testing.T) {
	db := openMigrated(t, memDSN("reap-live"))
	s, clock := newTestService(t, db, NewRegistry(), nil)

	id := seedTask(t, db, seed{
		Kind: "k", Status: StatusRunning, Attempts: 1,
		LeaseUntil: clock.now().Add(time.Minute).UnixNano(),
		LeaseToken: "fresh", Worker: "alive",
	})

	if err := s.reapOnce(context.Background()); err != nil {
		t.Fatalf("reapOnce: %v", err)
	}
	if got := readTask(t, db, id).Status; got != StatusRunning {
		t.Errorf("status = %q, want running: the lease has not expired", got)
	}
	if att := attemptsOf(t, db, id); len(att) != 0 {
		t.Errorf("attempts = %+v, want none", att)
	}
}

// Several instances reaping at once must not double-report. The compare-and-swap on
// the lease token is what stops them, so the second pass over the same row is a
// no-op — including no second attempts row.
func TestReapOnce_IsIdempotent(t *testing.T) {
	db := openMigrated(t, memDSN("reap-idempotent"))
	ctx := context.Background()
	s, clock := newTestService(t, db, NewRegistry(), nil)

	id := seedTask(t, db, seed{
		Kind: "k", Status: StatusRunning, Attempts: 1, MaxAttempts: 3,
		LeaseUntil: clock.now().Add(-time.Minute).UnixNano(),
		LeaseToken: "tok", Worker: "gone",
	})

	for range 3 {
		if err := s.reapOnce(ctx); err != nil {
			t.Fatalf("reapOnce: %v", err)
		}
	}
	if att := attemptsOf(t, db, id); len(att) != 1 {
		t.Errorf("%d attempt rows after three reaps, want 1", len(att))
	}
	if got := readTask(t, db, id).Attempts; got != 1 {
		t.Errorf("attempts = %d, want 1: reaping must not accumulate", got)
	}
}

// --- orphans ---

// A task whose kind has no handler is never claimed, so without this it would grow in
// silence. It is left alone for the grace period — which has to outlast a rolling
// deployment, or instances would kill each other's work — and then declared dead with
// a reason a human can act on.
func TestSweepOrphans_KillsAnOrphanAfterTheGracePeriod(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-sweep"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("known", noop)
	s, clock := newTestService(t, db, reg, &Config{
		Concurrency: ptr(1), OrphanGrace: 24 * time.Hour,
	})

	old := seedTask(t, db, seed{Kind: "typo:kind",
		CreatedAt: clock.now().Add(-25 * time.Hour).UnixNano()})
	recent := seedTask(t, db, seed{Kind: "typo:kind",
		CreatedAt: clock.now().Add(-time.Hour).UnixNano()})
	known := seedTask(t, db, seed{Kind: "known",
		CreatedAt: clock.now().Add(-25 * time.Hour).UnixNano()})

	if err := s.sweepOrphans(ctx); err != nil {
		t.Fatalf("sweepOrphans: %v", err)
	}

	if got := readTask(t, db, old).Status; got != StatusDead {
		t.Errorf("the old orphan is %q, want dead", got)
	}
	if got := readTask(t, db, recent).Status; got != StatusPending {
		t.Errorf("the recent orphan is %q, want pending: the grace period protects a "+
			"rolling deployment", got)
	}
	if got := readTask(t, db, known).Status; got != StatusPending {
		t.Errorf("a task with a handler is %q, want pending", got)
	}

	// The reason has to name the kind, or the admin screen shows a dead task with
	// nothing to act on.
	row := readTask(t, db, old)
	if !strings.Contains(row.LastError, "typo:kind") {
		t.Errorf("last_error = %q, want it to name the kind", row.LastError)
	}
	att := attemptsOf(t, db, old)
	if len(att) != 1 {
		t.Fatalf("attempts = %+v, want one explaining why it stopped", att)
	}
	// Attempt 0 because it never ran: claiming is what consumes an attempt number, and
	// this task was never claimed.
	if att[0].Attempt != 0 {
		t.Errorf("attempt number = %d, want 0: an orphan never ran", att[0].Attempt)
	}
	if !strings.Contains(att[0].Error, "typo:kind") {
		t.Errorf("attempt error = %q, want it to name the kind", att[0].Error)
	}
}

// Killing requires the whole application's registry. A worker holding a subset would
// declare other instances' work dead.
func TestSweepOrphans_IncompleteRegistryOnlyWarns(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-partial"))

	reg := NewRegistry()
	reg.complete = false
	s, clock := newTestService(t, db, reg, &Config{
		Concurrency: ptr(1), OrphanGrace: time.Hour,
	})

	id := seedTask(t, db, seed{Kind: "other:kind",
		CreatedAt: clock.now().Add(-48 * time.Hour).UnixNano()})

	if err := s.sweepOrphans(context.Background()); err != nil {
		t.Fatalf("sweepOrphans: %v", err)
	}
	if got := readTask(t, db, id).Status; got != StatusPending {
		t.Errorf("status = %q, want pending: a partial registry must not kill work it "+
			"cannot see", got)
	}
}

// --- pruning ---

func TestPruneOnce_EnforcesTheRetentionWindows(t *testing.T) {
	db := openMigrated(t, memDSN("prune-windows"))
	ctx := context.Background()
	s, clock := newTestService(t, db, NewRegistry(), &Config{
		Concurrency:        ptr(1),
		RetentionSucceeded: 7 * 24 * time.Hour,
		RetentionDead:      30 * 24 * time.Hour,
	})

	long := func(d time.Duration) int64 { return clock.now().Add(-d).UnixNano() }

	oldOK := seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: long(8 * 24 * time.Hour)})
	newOK := seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: long(24 * time.Hour)})
	oldCancelled := seedTask(t, db, seed{Status: StatusCancelled, FinishedAt: long(8 * 24 * time.Hour)})
	// Dead rows are kept much longer: they are the ones somebody still has to look at.
	deadInWindow := seedTask(t, db, seed{Status: StatusDead, FinishedAt: long(10 * 24 * time.Hour)})
	oldDead := seedTask(t, db, seed{Status: StatusDead, FinishedAt: long(31 * 24 * time.Hour)})
	// Never pruned, however old.
	ancientPending := seedTask(t, db, seed{Status: StatusPending, CreatedAt: 0})
	stuckRunning := seedTask(t, db, seed{Status: StatusRunning, CreatedAt: 0, LeaseToken: "t"})

	if err := insertAttempt(ctx, db, attemptRow{TaskID: oldOK, Attempt: 1, Outcome: OutcomeSucceeded}); err != nil {
		t.Fatal(err)
	}

	if err := s.pruneOnce(ctx); err != nil {
		t.Fatalf("pruneOnce: %v", err)
	}

	gone := []int64{oldOK, oldCancelled, oldDead}
	kept := []int64{newOK, deadInWindow, ancientPending, stuckRunning}
	for _, id := range gone {
		if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, id); n != 0 {
			t.Errorf("task %d survived the prune", id)
		}
	}
	for _, id := range kept {
		if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, id); n != 1 {
			t.Errorf("task %d was pruned but should have been kept", id)
		}
	}
	// A pruned task takes its failure log with it.
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_attempts WHERE task_id = ?`, oldOK); n != 0 {
		t.Error("the pruned task's attempts survived")
	}
}

// Deleting attempts and tasks is two statements by design, so an interruption between
// them leaves log rows with no task. The prune sweep collects them.
func TestPruneOnce_CollectsOrphanedAttempts(t *testing.T) {
	db := openMigrated(t, memDSN("prune-orphan-attempts"))
	ctx := context.Background()
	s, _ := newTestService(t, db, NewRegistry(), nil)

	live := seedTask(t, db, seed{Status: StatusPending})
	for _, taskID := range []int64{live, 999_999} {
		if err := insertAttempt(ctx, db, attemptRow{TaskID: taskID, Attempt: 1,
			Outcome: OutcomeFailed}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.pruneOnce(ctx); err != nil {
		t.Fatalf("pruneOnce: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_attempts`); n != 1 {
		t.Errorf("%d attempts left, want only the live task's", n)
	}
}

// One tick's work has to be bounded, or a first prune against a table with a year of
// history holds a lock for minutes. The cap is pruneMaxBatches × pruneBatch.
func TestPruneOnce_WorkPerTickIsBounded(t *testing.T) {
	db := openMigrated(t, memDSN("prune-bounded"))
	ctx := context.Background()
	s, clock := newTestService(t, db, NewRegistry(), &Config{
		Concurrency: ptr(1), RetentionSucceeded: time.Hour, RetentionDead: time.Hour,
	})

	// One more than a single tick can delete for this status.
	total := pruneMaxBatches*pruneBatch + 10
	old := clock.now().Add(-2 * time.Hour).UnixNano()
	for range total {
		seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: old})
	}

	if err := s.pruneOnce(ctx); err != nil {
		t.Fatalf("pruneOnce: %v", err)
	}
	left := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`)
	if left != 10 {
		t.Errorf("%d tasks left after one tick, want 10 (the cap deferred them)", left)
	}
	// And the next tick finishes the job, so nothing is stranded.
	if err := s.pruneOnce(ctx); err != nil {
		t.Fatalf("second pruneOnce: %v", err)
	}
	if left := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); left != 0 {
		t.Errorf("%d tasks left after a second tick, want 0", left)
	}
}
