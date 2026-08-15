package queue

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// A fixed clock. Every store function takes "now" as an argument, so no test here
// consults the wall clock or sleeps: a backoff is asserted by reading run_at, not
// by waiting for it.
const (
	t0     = int64(1_700_000_000_000_000_000) // an arbitrary UnixNano
	minute = int64(time.Minute)
)

func TestInsertTask(t *testing.T) {
	db := openMigrated(t, memDSN("insert"))
	ctx := context.Background()

	id, err := insertTask(ctx, db, enqueueParams{
		Kind:        "mail:welcome",
		Payload:     []byte(`{"user_id":7}`),
		RunAt:       t0 + minute,
		Priority:    5,
		MaxAttempts: 4,
		TimeoutMS:   30_000,
	}, t0)
	if err != nil {
		t.Fatalf("insertTask: %v", err)
	}
	if id == 0 {
		t.Fatal("insertTask returned id 0")
	}

	var (
		kind, payload, status, leaseToken, worker, lastErr string
		priority, attempts, maxAttempts, timeoutMS         int
		runAt, leaseUntil, createdAt, startedAt, finished  int64
	)
	err = db.QueryRowContext(ctx,
		`SELECT kind, payload, status, priority, run_at, attempts, max_attempts, timeout_ms,
		        lease_until, lease_token, worker, last_error, created_at, started_at, finished_at
		   FROM queue_tasks WHERE id = ?`, id).
		Scan(&kind, &payload, &status, &priority, &runAt, &attempts, &maxAttempts,
			&timeoutMS, &leaseUntil, &leaseToken, &worker, &lastErr, &createdAt,
			&startedAt, &finished)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if kind != "mail:welcome" || payload != `{"user_id":7}` || status != StatusPending {
		t.Errorf("kind/payload/status = %q/%q/%q", kind, payload, status)
	}
	if priority != 5 || maxAttempts != 4 || timeoutMS != 30_000 || runAt != t0+minute {
		t.Errorf("priority=%d maxAttempts=%d timeoutMS=%d runAt=%d", priority, maxAttempts, timeoutMS, runAt)
	}
	// A fresh task holds no lease, has not started, and carries no error.
	if attempts != 0 || leaseUntil != 0 || leaseToken != "" || worker != "" ||
		startedAt != 0 || finished != 0 || lastErr != "" {
		t.Errorf("a new task is not idle: attempts=%d lease=%d/%q worker=%q started=%d finished=%d lastErr=%q",
			attempts, leaseUntil, leaseToken, worker, startedAt, finished, lastErr)
	}
	if createdAt != t0 {
		t.Errorf("created_at = %d, want the injected now %d", createdAt, t0)
	}
}

// An absent schedule_id must be SQL NULL, not 0: 0 would read as a reference to a
// schedule that does not exist, and the admin screen's join would find nothing
// while claiming there is a plan.
func TestInsertTask_AbsentScheduleIDIsNull(t *testing.T) {
	db := openMigrated(t, memDSN("insert-null-sched"))
	ctx := context.Background()

	if _, err := insertTask(ctx, db, enqueueParams{Kind: "k", Payload: []byte("{}")}, t0); err != nil {
		t.Fatalf("insertTask: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE schedule_id IS NULL`); n != 1 {
		t.Errorf("%d rows with a NULL schedule_id, want 1", n)
	}

	if _, err := insertTask(ctx, db,
		enqueueParams{Kind: "k", Payload: []byte("{}"), ScheduleID: 42}, t0); err != nil {
		t.Fatalf("insertTask with a schedule: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE schedule_id = 42`); n != 1 {
		t.Errorf("%d rows with schedule_id 42, want 1", n)
	}
}

// The same for unique_key: "" has to become NULL, or the second task without a key
// collides with the first. This is the one place where getting a nullable column
// wrong is not cosmetic — it breaks every un-keyed enqueue after the first.
func TestInsertTask_EmptyUniqueKeyDoesNotDeduplicate(t *testing.T) {
	db := openMigrated(t, memDSN("insert-null-key"))
	ctx := context.Background()

	for range 3 {
		if _, err := insertTask(ctx, db, enqueueParams{Kind: "k", Payload: []byte("{}")}, t0); err != nil {
			t.Fatalf("insertTask without a unique key: %v", err)
		}
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 3 {
		t.Errorf("%d tasks, want 3", n)
	}

	// And a real key does deduplicate.
	p := enqueueParams{Kind: "k", Payload: []byte("{}"), UniqueKey: "cron:1:100"}
	if _, err := insertTask(ctx, db, p, t0); err != nil {
		t.Fatalf("first keyed insert: %v", err)
	}
	if _, err := insertTask(ctx, db, p, t0); err == nil {
		t.Error("a duplicate unique_key was accepted")
	}
}

// Only PostgreSQL asks for the id back as a row, and it has to.
//
// This is the assertion that would have caught the original: insertTask reported
// (0, nil) whenever LastInsertId failed, which on PostgreSQL — the dialect this
// template ships a driver for — is EVERY enqueue. The admin area read
// TriggerSchedule's 0 as "this plan no longer exists" and said so to the operator,
// the plan's last_task_id was stored as 0 and never resolved to a status, and
// `queue enqueue` printed "enqueued task 0".
func TestInsertTaskStmt_PerFlavor(t *testing.T) {
	cases := []struct {
		flavor    sqldb.Flavor
		returnsID bool
	}{
		{sqldb.PostgreSQL, true}, // no LastInsertId in its driver
		{sqldb.SQLite, false},    // accepts RETURNING since 3.35, but does not need it
		{sqldb.MySQL, false},     // has never had RETURNING
	}
	for _, tc := range cases {
		stmt, returnsID := insertTaskStmt(tc.flavor)
		if returnsID != tc.returnsID {
			t.Errorf("%s: returnsID = %v, want %v", tc.flavor, returnsID, tc.returnsID)
		}
		if got := strings.Contains(stmt, "RETURNING id"); got != tc.returnsID {
			t.Errorf("%s: statement has RETURNING = %v, want %v:\n%s", tc.flavor, got, tc.returnsID, stmt)
		}
	}
}

// And the statement that carries RETURNING really does yield the row's id, rather
// than being merely well-formed.
//
// Flipping Flavor on a SQLite handle is the same trick TestUpsertSQL_PerFlavor uses,
// and it is honest here: SQLite accepts both the ordinal placeholders sqldb rewrites
// to and the RETURNING clause, so this really does run the PostgreSQL statement. What
// it cannot prove is pgx's behaviour — only that the branch exists, is taken, and
// yields the id rather than zero.
func TestInsertTask_ReturnsTheIDOnPostgreSQL(t *testing.T) {
	db := openMigrated(t, memDSN("insert-returning"))
	db.Flavor = sqldb.PostgreSQL
	ctx := context.Background()

	var last int64
	for range 3 {
		id, err := insertTask(ctx, db, enqueueParams{Kind: "k", Payload: []byte("{}")}, t0)
		if err != nil {
			t.Fatalf("insertTask: %v", err)
		}
		if id == 0 {
			t.Fatal("insertTask returned id 0: callers read that as 'no such row'")
		}
		if id <= last {
			t.Errorf("id %d did not advance past %d: RETURNING gave the wrong row", id, last)
		}
		last = id
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, last); n != 1 {
		t.Errorf("the returned id %d does not name a row", last)
	}
}

func TestClaimTasks_OnlyDueAndOnlyKnownKinds(t *testing.T) {
	db := openMigrated(t, memDSN("claim-filter"))
	ctx := context.Background()

	due := seedTask(t, db, seed{Kind: "known", RunAt: t0 - minute})
	alsoDue := seedTask(t, db, seed{Kind: "known", RunAt: t0})
	seedTask(t, db, seed{Kind: "known", RunAt: t0 + minute})   // not due yet
	seedTask(t, db, seed{Kind: "unknown", RunAt: t0 - minute}) // no handler here
	seedTask(t, db, seed{Kind: "known", RunAt: t0, Status: StatusRunning})
	seedTask(t, db, seed{Kind: "known", RunAt: t0, Status: StatusDead})

	got, err := claimTasks(ctx, db, []string{"known"}, 10, t0, t0+minute, "w1")
	if err != nil {
		t.Fatalf("claimTasks: %v", err)
	}
	ids := claimedIDs(got)
	if want := []int64{due, alsoDue}; !slices.Equal(ids, want) {
		t.Errorf("claimed %v, want %v (due, pending, and a kind we handle)", ids, want)
	}
}

// A worker with no handlers claims nothing and sends no query. Cheap to get wrong:
// an empty IN () list is a syntax error on every dialect.
func TestClaimTasks_EmptyRegistryClaimsNothing(t *testing.T) {
	db := openMigrated(t, memDSN("claim-empty-registry"))
	seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute})

	got, err := claimTasks(context.Background(), db, nil, 10, t0, t0+minute, "w1")
	if err != nil {
		t.Fatalf("claimTasks with no kinds: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("claimed %d tasks with an empty registry, want 0", len(got))
	}
}

func TestClaimTasks_SetsTheLeaseAndBurnsAnAttempt(t *testing.T) {
	db := openMigrated(t, memDSN("claim-lease"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute, Attempts: 1, MaxAttempts: 5})

	got, err := claimTasks(ctx, db, []string{"k"}, 1, t0, t0+minute, "worker-a")
	if err != nil {
		t.Fatalf("claimTasks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d, want 1", len(got))
	}
	c := got[0]

	// Attempt is the number of THIS attempt, already incremented — which is how a
	// task that kills the process still consumes one.
	if c.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2 (seeded at 1, incremented at claim)", c.Attempt)
	}
	if c.MaxAttempts != 5 || c.ID != id {
		t.Errorf("claimed = %+v", c)
	}
	if c.LeaseToken == "" {
		t.Error("no lease token; nothing would fence the completion write")
	}

	row := readTask(t, db, id)
	if row.Status != StatusRunning || row.Attempts != 2 {
		t.Errorf("row = %+v, want running with attempts 2", row)
	}
	if row.LeaseUntil != t0+minute || row.LeaseToken != c.LeaseToken || row.Worker != "worker-a" {
		t.Errorf("lease not recorded: %+v", row)
	}
}

// Each claim gets its own token. Reusing one across tasks (or across the same task
// twice) would let a stale worker fence-check successfully against a lease it no
// longer holds.
func TestClaimTasks_TokensAreUnique(t *testing.T) {
	db := openMigrated(t, memDSN("claim-tokens"))
	for range 5 {
		seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute})
	}

	got, err := claimTasks(context.Background(), db, []string{"k"}, 5, t0, t0+minute, "w")
	if err != nil {
		t.Fatalf("claimTasks: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.LeaseToken] {
			t.Fatalf("lease token %q reused", c.LeaseToken)
		}
		seen[c.LeaseToken] = true
	}
	if len(seen) != 5 {
		t.Errorf("%d distinct tokens for 5 claims", len(seen))
	}
}

// Highest priority first, then oldest run_at, then lowest id. The tie-breakers
// matter: without them the order is whatever the index returns, and a task can be
// starved indefinitely by newer arrivals.
func TestClaimTasks_Ordering(t *testing.T) {
	db := openMigrated(t, memDSN("claim-order"))

	low := seedTask(t, db, seed{Kind: "k", RunAt: t0 - 3*minute, Priority: 0})
	high := seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute, Priority: 9})
	oldest := seedTask(t, db, seed{Kind: "k", RunAt: t0 - 5*minute, Priority: 0})

	got, err := claimTasks(context.Background(), db, []string{"k"}, 10, t0, t0+minute, "w")
	if err != nil {
		t.Fatalf("claimTasks: %v", err)
	}
	if want := []int64{high, oldest, low}; !slices.Equal(claimedIDs(got), want) {
		t.Errorf("order = %v, want %v (priority desc, then run_at asc)", claimedIDs(got), want)
	}
}

func TestClaimTasks_RespectsTheLimit(t *testing.T) {
	db := openMigrated(t, memDSN("claim-limit"))
	for range 10 {
		seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute})
	}

	got, err := claimTasks(context.Background(), db, []string{"k"}, 3, t0, t0+minute, "w")
	if err != nil {
		t.Fatalf("claimTasks: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("claimed %d, want 3", len(got))
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE status = ?`, StatusPending); n != 7 {
		t.Errorf("%d still pending, want 7", n)
	}
}

// Claim exclusivity, bookkeeping half.
//
// With MaxOpenConns(1) the pool serialises every statement, so this does NOT test
// the engine's row locking — it tests the discipline that surrounds it: that each
// task is handed out exactly once across many claim rounds and many workers, that
// no id appears twice, and that the attempts column agrees with the number of
// hand-outs. The engine's half is TestClaimTasks_IsExclusiveUnderRealConcurrency.
func TestClaimTasks_IsExclusiveAcrossWorkers(t *testing.T) {
	db := openMigrated(t, memDSN("claim-exclusive"))
	ctx := context.Background()

	const tasks = 40
	for range tasks {
		seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute})
	}

	seen := map[int64]int{}
	total := 0
	for round := range 20 {
		for _, worker := range []string{"w1", "w2", "w3"} {
			got, err := claimTasks(ctx, db, []string{"k"}, 4, t0, t0+minute, worker)
			if err != nil {
				t.Fatalf("round %d, %s: %v", round, worker, err)
			}
			for _, c := range got {
				seen[c.ID]++
				total++
			}
		}
	}

	if total != tasks {
		t.Errorf("handed out %d claims for %d tasks", total, tasks)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("task %d was claimed %d times, want exactly 1", id, n)
		}
	}
	if len(seen) != tasks {
		t.Errorf("%d distinct tasks claimed, want %d", len(seen), tasks)
	}
	// Every task ran exactly once, so every attempts column is exactly 1.
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE attempts != 1`); n != 0 {
		t.Errorf("%d tasks have an attempts count other than 1", n)
	}
}

// Claim exclusivity, engine half.
//
// The only test here that uses a file database and more than one connection,
// because the compare-and-swap in claimTasks is a claim about how the engine
// evaluates an UPDATE's predicate under contention, and a single-connection pool
// cannot exhibit contention at all. See openMigratedConcurrent for the DSN
// parameters a SQLite deployment of this queue needs.
func TestClaimTasks_IsExclusiveUnderRealConcurrency(t *testing.T) {
	db := openMigratedConcurrent(t, 4)
	ctx := context.Background()

	const tasks = 200
	for range tasks {
		seedTask(t, db, seed{Kind: "k", RunAt: t0 - minute})
	}

	const workers = 4
	var mu sync.Mutex
	seen := map[int64]int{}

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			worker := "w" + string(rune('0'+w))
			for {
				got, err := claimTasks(ctx, db, []string{"k"}, 8, t0, t0+minute, worker)
				if err != nil {
					t.Errorf("%s: %v", worker, err)
					return
				}
				if len(got) == 0 {
					return
				}
				mu.Lock()
				for _, c := range got {
					seen[c.ID]++
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()

	if len(seen) != tasks {
		t.Errorf("%d distinct tasks claimed, want %d", len(seen), tasks)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("task %d claimed %d times under real concurrency, want exactly 1", id, n)
		}
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE attempts != 1`); n != 0 {
		t.Errorf("%d tasks have an attempts count other than 1", n)
	}
}

func TestFinishTask(t *testing.T) {
	db := openMigrated(t, memDSN("finish"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 1,
		LeaseUntil: t0 + minute, LeaseToken: "tok", Worker: "w"})

	held, err := finishTask(ctx, db, id, "tok", StatusSucceeded, "", t0+30*int64(time.Second))
	if err != nil {
		t.Fatalf("finishTask: %v", err)
	}
	if !held {
		t.Fatal("finishTask reported the lease was lost")
	}

	row := readTask(t, db, id)
	if row.Status != StatusSucceeded || row.FinishedAt == 0 {
		t.Errorf("row = %+v, want succeeded with a finished_at", row)
	}
	// The lease is released, so the reaper never looks at this row again.
	if row.LeaseUntil != 0 || row.LeaseToken != "" || row.Worker != "" {
		t.Errorf("lease not cleared: %+v", row)
	}
}

// Fencing. A worker that stalled long enough to be reaped must not be able to
// report a result — another worker may be redoing the task right now, and the row
// belongs to it.
func TestFinishTask_StaleTokenIsRefused(t *testing.T) {
	db := openMigrated(t, memDSN("finish-fenced"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 2,
		LeaseUntil: t0 + minute, LeaseToken: "new-owner", Worker: "w2"})

	held, err := finishTask(ctx, db, id, "stale-token", StatusSucceeded, "", t0)
	if err != nil {
		t.Fatalf("finishTask: %v", err)
	}
	if held {
		t.Fatal("a stale token was accepted; a reaped task could be marked succeeded")
	}
	row := readTask(t, db, id)
	if row.Status != StatusRunning || row.LeaseToken != "new-owner" {
		t.Errorf("the row was modified by a stale worker: %+v", row)
	}
}

// The same fence, applied to a task that is no longer running at all — the shape an
// admin cancellation leaves behind.
func TestFinishTask_NonRunningTaskIsRefused(t *testing.T) {
	db := openMigrated(t, memDSN("finish-cancelled"))
	id := seedTask(t, db, seed{Kind: "k", Status: StatusCancelled, LeaseToken: "tok"})

	held, err := finishTask(context.Background(), db, id, "tok", StatusSucceeded, "", t0)
	if err != nil {
		t.Fatalf("finishTask: %v", err)
	}
	if held {
		t.Fatal("a cancelled task was resurrected as succeeded")
	}
	if readTask(t, db, id).Status != StatusCancelled {
		t.Error("the cancelled status did not survive")
	}
}

func TestRetryTask_LeavesAttemptsAlone(t *testing.T) {
	db := openMigrated(t, memDSN("retry"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 2,
		LeaseUntil: t0 + minute, LeaseToken: "tok", Worker: "w"})

	held, err := retryTask(ctx, db, id, "tok", t0+5*minute, "dial tcp: timeout", t0)
	if err != nil {
		t.Fatalf("retryTask: %v", err)
	}
	if !held {
		t.Fatal("retryTask reported the lease was lost")
	}

	row := readTask(t, db, id)
	if row.Status != StatusPending || row.RunAt != t0+5*minute {
		t.Errorf("row = %+v, want pending with the backoff applied", row)
	}
	// This is the same attempt's aftermath, not a new one: attempts was already
	// incremented at claim time.
	if row.Attempts != 2 {
		t.Errorf("attempts = %d, want 2 unchanged", row.Attempts)
	}
	if row.LastError != "dial tcp: timeout" {
		t.Errorf("last_error = %q", row.LastError)
	}
	if row.FinishedAt != 0 {
		t.Errorf("finished_at = %d, want 0: a retried task has not finished", row.FinishedAt)
	}
	if row.LeaseUntil != 0 || row.LeaseToken != "" {
		t.Errorf("lease not cleared: %+v", row)
	}
}

func TestRetryTask_StaleTokenIsRefused(t *testing.T) {
	db := openMigrated(t, memDSN("retry-fenced"))
	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, LeaseToken: "owner"})

	held, err := retryTask(context.Background(), db, id, "stale", t0, "", t0)
	if err != nil {
		t.Fatalf("retryTask: %v", err)
	}
	if held {
		t.Error("a stale token rescheduled a task another worker owns")
	}
}

// releaseTask refunds the attempt, and it is the ONLY path that does. The
// difference from the reaper is knowledge: this process cancelled the handler
// itself and is about to be replaced by one that can pick the task up immediately,
// so charging the operator's deployment a retry charges it for nothing.
func TestReleaseTask_RefundsTheAttempt(t *testing.T) {
	db := openMigrated(t, memDSN("release"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 2,
		LeaseUntil: t0 + minute, LeaseToken: "tok", Worker: "w"})

	held, err := releaseTask(ctx, db, id, "tok", t0, "shutting down", t0)
	if err != nil {
		t.Fatalf("releaseTask: %v", err)
	}
	if !held {
		t.Fatal("releaseTask reported the lease was lost")
	}

	row := readTask(t, db, id)
	if row.Status != StatusPending || row.RunAt != t0 {
		t.Errorf("row = %+v, want pending and immediately eligible", row)
	}
	if row.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (refunded from 2)", row.Attempts)
	}
}

// The refund is floored at zero. attempts is read back from the database, and a
// negative count would make every subsequent backoff computation nonsense.
func TestReleaseTask_NeverGoesNegative(t *testing.T) {
	db := openMigrated(t, memDSN("release-floor"))
	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 0, LeaseToken: "tok"})

	if _, err := releaseTask(context.Background(), db, id, "tok", t0, "", t0); err != nil {
		t.Fatalf("releaseTask: %v", err)
	}
	if got := readTask(t, db, id).Attempts; got != 0 {
		t.Errorf("attempts = %d, want 0", got)
	}
}

func TestInsertAttempt(t *testing.T) {
	db := openMigrated(t, memDSN("attempt"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k"})
	err := insertAttempt(ctx, db, attemptRow{
		TaskID: id, Attempt: 2, Outcome: OutcomeFailed, Worker: "w",
		Error: "boom", StartedAt: t0, FinishedAt: t0 + minute,
	})
	if err != nil {
		t.Fatalf("insertAttempt: %v", err)
	}

	var (
		attempt              int
		outcome, worker, msg string
		started, finished    int64
	)
	err = db.QueryRowContext(ctx,
		`SELECT attempt, outcome, worker, error, started_at, finished_at
		   FROM queue_attempts WHERE task_id = ?`, id).
		Scan(&attempt, &outcome, &worker, &msg, &started, &finished)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if attempt != 2 || outcome != OutcomeFailed || worker != "w" || msg != "boom" {
		t.Errorf("attempt row = %d/%q/%q/%q", attempt, outcome, worker, msg)
	}
	if started != t0 || finished != t0+minute {
		t.Errorf("timestamps = %d..%d", started, finished)
	}
}

// The failure log has to survive a handler that returns something enormous, and it
// has to store valid UTF-8: PostgreSQL rejects a broken sequence on a text column
// outright, so cutting mid-rune would turn a big error into a failed INSERT.
func TestInsertAttempt_TruncatesAHugeError(t *testing.T) {
	db := openMigrated(t, memDSN("attempt-huge"))
	ctx := context.Background()

	id := seedTask(t, db, seed{Kind: "k"})
	huge := strings.Repeat("失败", 20_000) // multi-byte on purpose
	if err := insertAttempt(ctx, db, attemptRow{
		TaskID: id, Attempt: 1, Outcome: OutcomeFailed, Error: huge,
	}); err != nil {
		t.Fatalf("insertAttempt: %v", err)
	}

	var stored string
	if err := db.QueryRowContext(ctx,
		`SELECT error FROM queue_attempts WHERE task_id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored) > maxAttemptErrBytes {
		t.Errorf("stored %d bytes, want at most %d", len(stored), maxAttemptErrBytes)
	}
	if !strings.HasSuffix(stored, "…") {
		t.Error("a truncated error should say so")
	}
}

// The heartbeat's return value is the cooperative-cancellation signal: the ids it
// could NOT renew are the ones somebody else took, and their handlers' contexts
// should be cancelled. Obtained for free from a statement that had to run anyway.
func TestRenewLeases_ReportsWhatItLost(t *testing.T) {
	db := openMigrated(t, memDSN("renew"))
	ctx := context.Background()

	mine := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: t0, LeaseToken: "a", Worker: "w1"})
	alsoMine := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: t0, LeaseToken: "b", Worker: "w1"})
	reaped := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: t0, LeaseToken: "c", Worker: "w2"}) // another worker owns it now
	cancelled := seedTask(t, db, seed{Kind: "k", Status: StatusCancelled, Worker: "w1"})

	held := []leaseHold{
		{mine, "a"}, {alsoMine, "b"}, {reaped, "c"}, {cancelled, ""},
	}
	lost, err := renewLeases(ctx, db, held, "w1", t0+5*minute, t0)
	if err != nil {
		t.Fatalf("renewLeases: %v", err)
	}

	want := []leaseHold{{reaped, "c"}, {cancelled, ""}}
	sortHolds := func(hs []leaseHold) {
		slices.SortFunc(hs, func(a, b leaseHold) int { return int(a.ID - b.ID) })
	}
	sortHolds(lost)
	sortHolds(want)
	if !slices.Equal(lost, want) {
		t.Errorf("lost = %v, want %v", lost, want)
	}

	// The ones we kept were extended; the ones we lost were not touched.
	if got := readTask(t, db, mine).LeaseUntil; got != t0+5*minute {
		t.Errorf("kept lease not extended: %d", got)
	}
	if got := readTask(t, db, reaped).LeaseUntil; got != t0 {
		t.Errorf("another worker's lease was extended: %d", got)
	}
}

// One process can hold two executions of the SAME task, and the stale one has to be
// the one reported lost.
//
// How it happens: this process's heartbeats fail through a database blip, another
// instance's reaper returns the task to pending, and this process's poller — which
// runs far more often than its heartbeat — claims it again under a new token before
// its own heartbeat notices the loss. The row is legitimately ours in both cases, so
// matching on the id alone renews the dead execution forever and never cancels it.
func TestRenewLeases_ReportsASupersededExecutionOfTheSameTask(t *testing.T) {
	db := openMigrated(t, memDSN("renew-superseded"))
	ctx := context.Background()

	// The row as it stands after the re-claim: still ours, but under token "new".
	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: t0, LeaseToken: "new", Worker: "w1"})

	stale := leaseHold{id, "old"}
	live := leaseHold{id, "new"}
	lost, err := renewLeases(ctx, db, []leaseHold{stale, live}, "w1", t0+5*minute, t0)
	if err != nil {
		t.Fatalf("renewLeases: %v", err)
	}
	if !slices.Equal(lost, []leaseHold{stale}) {
		t.Errorf("lost = %v, want only the superseded execution %v", lost, stale)
	}
	// And the row itself was still renewed — the live execution keeps working.
	if got := readTask(t, db, id).LeaseUntil; got != t0+5*minute {
		t.Errorf("the live execution's lease was not extended: %d", got)
	}
}

func TestRenewLeases_NoIDsIsANoop(t *testing.T) {
	db := openMigrated(t, memDSN("renew-empty"))
	lost, err := renewLeases(context.Background(), db, nil, "w", t0, t0)
	if err != nil || lost != nil {
		t.Errorf("renewLeases(nil) = %v, %v; want nil, nil", lost, err)
	}
}

func TestExpiredLeases(t *testing.T) {
	db := openMigrated(t, memDSN("expired"))

	stale := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, Attempts: 1,
		LeaseUntil: t0 - minute, LeaseToken: "old", Worker: "gone"})
	// A live lease and an idle task, both of which must be left alone.
	seedTask(t, db, seed{Kind: "k", Status: StatusRunning,
		LeaseUntil: t0 + minute, LeaseToken: "fresh", Worker: "here"})
	seedTask(t, db, seed{Kind: "k", Status: StatusPending}) // lease_until 0

	got, err := expiredLeases(context.Background(), db, t0, 10)
	if err != nil {
		t.Fatalf("expiredLeases: %v", err)
	}
	if len(got) != 1 || got[0].ID != stale {
		t.Fatalf("expiredLeases = %+v, want just task %d", got, stale)
	}
	// The reaper needs all of these to decide between pending and dead, and to
	// write an honest attempts row for the worker that vanished.
	if got[0].Attempts != 1 || got[0].MaxAttempts != 3 ||
		got[0].LeaseToken != "old" || got[0].Worker != "gone" {
		t.Errorf("expired lease = %+v", got[0])
	}
}

// lease_until > 0 is part of the predicate, not decoration: an idle task has
// lease_until 0, which is <= any now and would otherwise be reaped forever.
func TestExpiredLeases_IgnoresAZeroLease(t *testing.T) {
	db := openMigrated(t, memDSN("expired-zero"))
	seedTask(t, db, seed{Kind: "k", Status: StatusRunning, LeaseUntil: 0})

	got, err := expiredLeases(context.Background(), db, t0, 10)
	if err != nil {
		t.Fatalf("expiredLeases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a running row with no lease was reported expired: %+v", got)
	}
}

func TestOrphanCounts(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-count"))

	seedTask(t, db, seed{Kind: "known"})
	seedTask(t, db, seed{Kind: "gone"})
	seedTask(t, db, seed{Kind: "gone"})
	seedTask(t, db, seed{Kind: "also-gone"})
	// Terminal rows are not orphans — they are not waiting for anything.
	seedTask(t, db, seed{Kind: "gone", Status: StatusDead})

	got, err := orphanCounts(context.Background(), db, []string{"known"})
	if err != nil {
		t.Fatalf("orphanCounts: %v", err)
	}
	if len(got) != 2 || got["gone"] != 2 || got["also-gone"] != 1 {
		t.Errorf("orphanCounts = %v, want gone:2 also-gone:1", got)
	}
}

// A process with no handlers has no way to run anything, so every pending task is
// an orphan from its point of view. The correct answer, and the one an empty
// IN () list would turn into a syntax error.
func TestOrphanCounts_EmptyKindsMeansEverything(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-count-empty"))
	seedTask(t, db, seed{Kind: "a"})
	seedTask(t, db, seed{Kind: "b"})

	got, err := orphanCounts(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("orphanCounts: %v", err)
	}
	if got["a"] != 1 || got["b"] != 1 {
		t.Errorf("orphanCounts = %v, want every pending kind", got)
	}
}

func TestOrphanIDsAndKillPending(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-kill"))
	ctx := context.Background()

	old := seedTask(t, db, seed{Kind: "gone", CreatedAt: t0 - 25*int64(time.Hour)})
	seedTask(t, db, seed{Kind: "gone", CreatedAt: t0})                        // inside the grace period
	seedTask(t, db, seed{Kind: "known", CreatedAt: t0 - 25*int64(time.Hour)}) // has a handler

	cutoff := t0 - 24*int64(time.Hour)
	orphans, err := orphanTasks(ctx, db, []string{"known"}, cutoff, 100)
	if err != nil {
		t.Fatalf("orphanTasks: %v", err)
	}
	if len(orphans) != 1 || orphans[0].ID != old {
		t.Fatalf("orphanTasks = %+v, want just task %d", orphans, old)
	}
	// The kind rides along so the caller can name it in the failure log without a
	// second query per orphan.
	if orphans[0].Kind != "gone" {
		t.Errorf("orphan kind = %q, want %q", orphans[0].Kind, "gone")
	}

	changed, err := killPending(ctx, db, old, `queue: no handler for kind "gone"`, t0)
	if err != nil {
		t.Fatalf("killPending: %v", err)
	}
	if !changed {
		t.Fatal("killPending reported no change")
	}
	row := readTask(t, db, old)
	if row.Status != StatusDead || row.FinishedAt != t0 {
		t.Errorf("row = %+v, want dead with a finished_at", row)
	}
	if !strings.Contains(row.LastError, "gone") {
		t.Errorf("last_error %q should name the kind so the screen can explain it", row.LastError)
	}
}

// killPending is fenced on the status: a task claimed between the scan and the kill
// must not be declared dead out from under the worker now running it.
func TestKillPending_RefusesARunningTask(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-kill-running"))
	id := seedTask(t, db, seed{Kind: "k", Status: StatusRunning, LeaseToken: "tok"})

	changed, err := killPending(context.Background(), db, id, "x", t0)
	if err != nil {
		t.Fatalf("killPending: %v", err)
	}
	if changed {
		t.Error("killPending killed a running task")
	}
	if readTask(t, db, id).Status != StatusRunning {
		t.Error("the running status did not survive")
	}
}

func TestPrunableIDsAndDeleteTasks(t *testing.T) {
	db := openMigrated(t, memDSN("prune"))
	ctx := context.Background()

	cutoff := t0
	oldSucceeded := seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: t0 - minute})
	seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: t0 + minute}) // too new
	seedTask(t, db, seed{Status: StatusDead, FinishedAt: t0 - minute})      // other status
	// Pending is never pruned, however old — an orphan is killed, not deleted.
	seedTask(t, db, seed{Status: StatusPending, CreatedAt: 0, FinishedAt: 0})

	if err := insertAttempt(ctx, db, attemptRow{TaskID: oldSucceeded, Attempt: 1,
		Outcome: OutcomeSucceeded}); err != nil {
		t.Fatalf("insertAttempt: %v", err)
	}

	ids, err := prunableIDs(ctx, db, StatusSucceeded, cutoff, 100)
	if err != nil {
		t.Fatalf("prunableIDs: %v", err)
	}
	if !slices.Equal(ids, []int64{oldSucceeded}) {
		t.Fatalf("prunableIDs = %v, want [%d]", ids, oldSucceeded)
	}

	if err := deleteTasks(ctx, db, ids); err != nil {
		t.Fatalf("deleteTasks: %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, oldSucceeded); n != 0 {
		t.Error("the task survived")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_attempts WHERE task_id = ?`, oldSucceeded); n != 0 {
		t.Error("its attempts survived")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 3 {
		t.Errorf("%d tasks left, want 3", n)
	}
}

// finished_at > 0 is part of the predicate: a terminal row that somehow has no
// finish stamp must not be deleted by a cutoff comparison against zero.
func TestPrunableIDs_IgnoresAMissingFinishStamp(t *testing.T) {
	db := openMigrated(t, memDSN("prune-nofinish"))
	seedTask(t, db, seed{Status: StatusSucceeded, FinishedAt: 0})

	ids, err := prunableIDs(context.Background(), db, StatusSucceeded, t0, 100)
	if err != nil {
		t.Fatalf("prunableIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("prunableIDs = %v, want none", ids)
	}
}

func TestDeleteTasks_EmptyIsANoop(t *testing.T) {
	db := openMigrated(t, memDSN("delete-empty"))
	seedTask(t, db, seed{})
	if err := deleteTasks(context.Background(), db, nil); err != nil {
		t.Fatalf("deleteTasks(nil): %v", err)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`); n != 1 {
		t.Errorf("deleteTasks(nil) deleted something: %d rows left", n)
	}
}

// Attempts and tasks are deleted as two statements by design, so an interruption
// between them leaves log rows with no task. This is the sweep that collects them.
func TestDeleteOrphanAttempts(t *testing.T) {
	db := openMigrated(t, memDSN("orphan-attempts"))
	ctx := context.Background()

	live := seedTask(t, db, seed{})
	if err := insertAttempt(ctx, db, attemptRow{TaskID: live, Attempt: 1, Outcome: OutcomeSucceeded}); err != nil {
		t.Fatal(err)
	}
	if err := insertAttempt(ctx, db, attemptRow{TaskID: 9999, Attempt: 1, Outcome: OutcomeFailed}); err != nil {
		t.Fatal(err)
	}

	n, err := deleteOrphanAttempts(ctx, db, 100)
	if err != nil {
		t.Fatalf("deleteOrphanAttempts: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM queue_attempts`); got != 1 {
		t.Errorf("%d attempts left, want the live one", got)
	}
}

func TestPlaceholders(t *testing.T) {
	cases := map[int]string{0: "", -1: "", 1: "?", 2: "?, ?", 3: "?, ?, ?"}
	for n, want := range cases {
		if got := placeholders(n); got != want {
			t.Errorf("placeholders(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate left a short string alone incorrectly: %q", got)
	}
	// Multi-byte input must not be cut mid-rune: PostgreSQL rejects invalid UTF-8
	// on a text column, so a bad cut turns a big error into a failed INSERT.
	long := strings.Repeat("失", 100) // 3 bytes each
	got := truncate(long, 10)
	if len(got) > 10 {
		t.Errorf("truncate(%d bytes) = %d bytes, want at most 10", len(long), len(got))
	}
	if !utf8ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q, want a visible ellipsis", got)
	}
	// A limit smaller than the ellipsis itself must still return valid UTF-8.
	if tiny := truncate(long, 1); !utf8ValidString(tiny) {
		t.Errorf("truncate to 1 byte = %q, not valid UTF-8", tiny)
	}
}

func TestNullableHelpers(t *testing.T) {
	if nullableID(0) != nil {
		t.Error("nullableID(0) must be NULL, not a reference to schedule 0")
	}
	if nullableID(7) != any(int64(7)) {
		t.Errorf("nullableID(7) = %v", nullableID(7))
	}
	if nullableText("") != nil {
		t.Error(`nullableText("") must be NULL, or the second un-keyed task collides with the first`)
	}
	if nullableText("k") != any("k") {
		t.Errorf("nullableText(%q) = %v", "k", nullableText("k"))
	}
}

func TestNewLeaseToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, err := newLeaseToken()
		if err != nil {
			t.Fatalf("newLeaseToken: %v", err)
		}
		// 32 characters, matching the MySQL column.
		if len(tok) != 32 {
			t.Fatalf("token %q is %d characters, want 32", tok, len(tok))
		}
		if seen[tok] {
			t.Fatalf("token %q repeated", tok)
		}
		seen[tok] = true
	}
}

func TestTerminalStatuses(t *testing.T) {
	// A guard against adding a status without deciding whether it is terminal:
	// the pruner deletes only these, and the admin retry action accepts only these.
	want := []string{StatusSucceeded, StatusDead, StatusCancelled}
	if !slices.Equal(terminalStatuses, want) {
		t.Errorf("terminalStatuses = %v, want %v", terminalStatuses, want)
	}
	if slices.Contains(terminalStatuses, StatusPending) || slices.Contains(terminalStatuses, StatusRunning) {
		t.Error("pending or running is listed as terminal; the pruner would delete live work")
	}
}

func claimedIDs(cs []claimedTask) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
