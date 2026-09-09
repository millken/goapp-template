package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// attemptState is the subset of an attempt row the assertions read.
type attemptState struct {
	Attempt int
	Outcome string
	Error   string
	Worker  string
}

// attemptsOf returns a task's attempt log, oldest first — which is what the failure
// log on the detail screen shows.
func attemptsOf(t *testing.T, db *sqldb.DB, taskID int64) []attemptState {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT attempt, outcome, error, worker FROM queue_attempts
		  WHERE task_id = ? ORDER BY id ASC`, taskID)
	if err != nil {
		t.Fatalf("read attempts of %d: %v", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []attemptState{}
	for rows.Next() {
		var a attemptState
		if err := rows.Scan(&a.Attempt, &a.Outcome, &a.Error, &a.Worker); err != nil {
			t.Fatalf("scan attempt: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read attempts of %d: %v", taskID, err)
	}
	return out
}

// --- runOne: the outcome matrix ---

func TestRunOne_Success(t *testing.T) {
	db := openMigrated(t, memDSN("run-ok"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error { return nil })
	s, clock := newTestService(t, db, reg, nil)

	id := seedTask(t, db, seed{Kind: "k", RunAt: 0})
	claimed := claimOne(t, s, id)
	s.runOne(context.Background(), claimed)

	row := readTask(t, db, id)
	if row.Status != StatusSucceeded {
		t.Errorf("status = %q, want succeeded", row.Status)
	}
	if row.FinishedAt != clock.now().UnixNano() {
		t.Errorf("finished_at = %d, want the injected now", row.FinishedAt)
	}
	if row.LeaseToken != "" || row.Worker != "" {
		t.Errorf("lease not released: %+v", row)
	}

	att := attemptsOf(t, db, id)
	if len(att) != 1 || att[0].Outcome != OutcomeSucceeded || att[0].Attempt != 1 {
		t.Errorf("attempts = %+v, want one succeeded attempt numbered 1", att)
	}
}

// A plain failure with attempts left goes back to pending, with the backoff written
// to run_at. Zero jitter in tests puts it on the floor of the equal-jitter band, so
// the expected value is exact.
func TestRunOne_FailureRetriesWithBackoff(t *testing.T) {
	db := openMigrated(t, memDSN("run-retry"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error { return errors.New("dial tcp: timeout") })
	s, clock := newTestService(t, db, reg, &Config{
		Concurrency: ptr(1), RetryBase: 15 * time.Second, RetryMax: time.Hour,
	})

	id := seedTask(t, db, seed{Kind: "k", MaxAttempts: 3})
	s.runOne(context.Background(), claimOne(t, s, id))

	row := readTask(t, db, id)
	if row.Status != StatusPending {
		t.Errorf("status = %q, want pending for a retryable failure", row.Status)
	}
	// attempt 1 → base 15s → equal jitter with zero → 7.5s
	wantRunAt := clock.now().Add(7500 * time.Millisecond).UnixNano()
	if row.RunAt != wantRunAt {
		t.Errorf("run_at = %d, want %d (half of the 15s base)", row.RunAt, wantRunAt)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: the claim consumed it, the retry does not add another", row.Attempts)
	}
	if row.LastError != "dial tcp: timeout" {
		t.Errorf("last_error = %q", row.LastError)
	}
	if row.FinishedAt != 0 {
		t.Errorf("finished_at = %d, want 0: a task waiting to retry has not finished", row.FinishedAt)
	}

	att := attemptsOf(t, db, id)
	if len(att) != 1 || att[0].Outcome != OutcomeFailed {
		t.Errorf("attempts = %+v, want one failed attempt", att)
	}
}

// The last attempt gives up. `dead` rather than `failed`, because a retryable failure
// went back to pending — so there is only one terminal failure and naming it "failed"
// would make the admin filter for it read as "everything that has ever failed".
func TestRunOne_LastAttemptGoesDead(t *testing.T) {
	db := openMigrated(t, memDSN("run-dead"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error { return errors.New("still broken") })
	s, _ := newTestService(t, db, reg, nil)

	// Already used two of three; the claim makes this the third and last.
	id := seedTask(t, db, seed{Kind: "k", Attempts: 2, MaxAttempts: 3})
	s.runOne(context.Background(), claimOne(t, s, id))

	row := readTask(t, db, id)
	if row.Status != StatusDead {
		t.Errorf("status = %q, want dead on the last attempt", row.Status)
	}
	if row.Attempts != 3 || row.FinishedAt == 0 {
		t.Errorf("row = %+v", row)
	}
}

// ErrPermanent skips the remaining attempts and the backoff they would have waited
// out: the input is wrong, so nothing about retrying can change the answer.
func TestRunOne_PermanentFailureSkipsRetries(t *testing.T) {
	db := openMigrated(t, memDSN("run-permanent"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error {
		return fmt.Errorf("bad address: %w", ErrPermanent)
	})
	s, _ := newTestService(t, db, reg, nil)

	id := seedTask(t, db, seed{Kind: "k", MaxAttempts: 10})
	s.runOne(context.Background(), claimOne(t, s, id))

	row := readTask(t, db, id)
	if row.Status != StatusDead {
		t.Errorf("status = %q, want dead: a permanent failure does not retry", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1: it must not have burned the other nine", row.Attempts)
	}
	if !strings.Contains(row.LastError, "bad address") {
		t.Errorf("last_error = %q, want the handler's own message", row.LastError)
	}
}

// A timeout is recorded as its own outcome. A slow dependency and a broken one need
// different responses, and the error text alone does not distinguish them.
func TestRunOne_TimeoutIsRecordedAsSuch(t *testing.T) {
	db := openMigrated(t, memDSN("run-timeout"))
	reg := NewRegistry()
	reg.Handle("k", func(ctx context.Context, _ *Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	s, _ := newTestService(t, db, reg, &Config{Concurrency: ptr(1)})

	// A per-task timeout short enough to expire at once.
	id := seedTask(t, db, seed{Kind: "k", MaxAttempts: 3})
	claimed := claimOne(t, s, id)
	claimed.TimeoutMS = 1
	s.runOne(context.Background(), claimed)

	att := attemptsOf(t, db, id)
	if len(att) != 1 || att[0].Outcome != OutcomeTimeout {
		t.Errorf("attempts = %+v, want one timeout", att)
	}
	if got := readTask(t, db, id).Status; got != StatusPending {
		t.Errorf("status = %q, want pending: a timeout is retryable", got)
	}
}

// A handler's panic must not take down the process — which, for the worker embedded
// in serve, is the web server. The stack has to survive into the log, or the failure
// is one nobody can act on from an admin screen.
func TestRunOne_PanicIsContainedWithItsStack(t *testing.T) {
	db := openMigrated(t, memDSN("run-panic"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error {
		panic("handler exploded")
	})
	s, _ := newTestService(t, db, reg, nil)

	id := seedTask(t, db, seed{Kind: "k", MaxAttempts: 3})
	s.runOne(context.Background(), claimOne(t, s, id))

	row := readTask(t, db, id)
	if row.Status != StatusPending {
		t.Errorf("status = %q, want pending: a panic is a retryable failure", row.Status)
	}
	att := attemptsOf(t, db, id)
	if len(att) != 1 {
		t.Fatalf("attempts = %+v, want one", att)
	}
	if !strings.Contains(att[0].Error, "handler exploded") {
		t.Errorf("attempt error %q should carry the panic value", att[0].Error)
	}
	if !strings.Contains(att[0].Error, "goroutine") {
		t.Errorf("attempt error should carry a stack; got %q", att[0].Error)
	}
}

// Fencing, at the level runOne sees it: a result for a task somebody else now owns is
// discarded, and — importantly — no attempts row is written, or the log would describe
// an execution whose result was thrown away.
func TestRunOne_DiscardsAResultForALostLease(t *testing.T) {
	db := openMigrated(t, memDSN("run-fenced"))
	ctx := context.Background()
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error { return nil })
	s, _ := newTestService(t, db, reg, nil)

	id := seedTask(t, db, seed{Kind: "k"})
	claimed := claimOne(t, s, id)

	// The reaper got there first and handed the task to someone else.
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_tasks SET lease_token = 'someone-else' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	s.runOne(ctx, claimed)

	if got := readTask(t, db, id).Status; got != StatusRunning {
		t.Errorf("status = %q, want running: the new owner's row must be untouched", got)
	}
	if att := attemptsOf(t, db, id); len(att) != 0 {
		t.Errorf("attempts = %+v, want none: a discarded result must leave no log entry", att)
	}
}

// --- shutdown ---

// A task interrupted by shutdown is handed straight back with its attempt REFUNDED.
// This is the only path that refunds: the interruption is known to be deliberate and
// a replacement process is starting, so charging the deployment a retry charges it
// for nothing.
func TestRunOne_ShutdownRefundsTheAttempt(t *testing.T) {
	db := openMigrated(t, memDSN("run-shutdown"))
	reg := NewRegistry()
	reg.Handle("k", func(ctx context.Context, _ *Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	s, clock := newTestService(t, db, reg, nil)

	id := seedTask(t, db, seed{Kind: "k", Attempts: 1, MaxAttempts: 3})
	claimed := claimOne(t, s, id) // attempts is now 2

	// The shutdown path: the PARENT context is cancelled, which is what tells runOne
	// this was our own doing rather than a timeout or a lost lease.
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	s.runOne(parent, claimed)

	row := readTask(t, db, id)
	if row.Status != StatusPending {
		t.Errorf("status = %q, want pending", row.Status)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 refunded from 2", row.Attempts)
	}
	if row.RunAt != clock.now().UnixNano() {
		t.Errorf("run_at = %d, want now: the replacement should pick it up at once", row.RunAt)
	}
	att := attemptsOf(t, db, id)
	if len(att) != 1 || att[0].Outcome != OutcomeCancelled {
		t.Errorf("attempts = %+v, want one cancelled attempt", att)
	}
}

// --- heartbeat ---

// The heartbeat extends what this process holds and cancels the handlers of what it
// has lost. That second half is the entire implementation of "cancel a running task"
// from the admin area: cancelling changes the status, the renewal then skips the row,
// and the handler's context is cancelled here.
func TestHeartbeatOnce_CancelsALostTask(t *testing.T) {
	db := openMigrated(t, memDSN("heartbeat"))
	ctx := context.Background()
	s, clock := newTestService(t, db, NewRegistry(), nil)

	kept := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: clock.now().UnixNano(), LeaseToken: "a", Worker: s.workerID})
	lost := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: clock.now().UnixNano(), LeaseToken: "b", Worker: s.workerID})

	var keptCancelled, lostCancelled atomic.Bool
	s.trackInFlight(leaseHold{kept, "a"}, func() { keptCancelled.Store(true) })
	s.trackInFlight(leaseHold{lost, "b"}, func() { lostCancelled.Store(true) })

	// Somebody cancels the second one from the admin area.
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_tasks SET status = ?, worker = '' WHERE id = ?`,
		StatusCancelled, lost); err != nil {
		t.Fatal(err)
	}

	if err := s.heartbeatOnce(ctx); err != nil {
		t.Fatalf("heartbeatOnce: %v", err)
	}

	if keptCancelled.Load() {
		t.Error("a task we still hold had its handler cancelled")
	}
	if !lostCancelled.Load() {
		t.Error("a task we lost did not have its handler cancelled")
	}
	if got := readTask(t, db, kept).LeaseUntil; got != clock.now().Add(s.LeaseTTL()).UnixNano() {
		t.Errorf("kept lease not extended: %d", got)
	}
}

func TestHeartbeatOnce_NothingInFlight(t *testing.T) {
	db := openMigrated(t, memDSN("heartbeat-idle"))
	s, _ := newTestService(t, db, NewRegistry(), nil)
	if err := s.heartbeatOnce(context.Background()); err != nil {
		t.Errorf("heartbeatOnce with nothing in flight: %v", err)
	}
}

// --- helpers ---

// claimOne claims the given task through the real claim path, so the test works with
// a genuine lease token rather than a pretend one.
func claimOne(t *testing.T, s *Service, id int64) claimedTask {
	t.Helper()
	batch, err := s.claimBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("claimBatch: %v", err)
	}
	for _, c := range batch {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("task %d was not claimed; claimed %v instead", id, claimedIDs(batch))
	return claimedTask{}
}
