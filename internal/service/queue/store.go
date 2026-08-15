package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dnsoa/go/sqldb"
)

// Every statement the worker path runs, in one file.
//
// These are free functions taking an explicit *sqldb.DB and an explicit "now"
// rather than methods on Service. Two reasons: a test can drive them with a
// handle and a fixed clock without constructing a service, and the absence of a
// receiver makes it obvious that none of them keep state — the state is all in
// the rows.
//
// Placeholders are always `?`; sqldb rewrites them to $1 for PostgreSQL. Values
// are always parameterised — nothing here interpolates a value into SQL, and the
// only interpolation at all is a placeholder list whose length comes from a Go
// slice (see placeholders).
//
// None of this uses a transaction, which is deliberate and load-bearing. The
// atomicity comes from the compare-and-swap in each UPDATE's WHERE clause, so a
// transaction would buy nothing while holding a connection across several
// statements — and sqldb.Transaction begins with context.Background(), making the
// transaction itself uncancellable (documented on Admin.keepingASuperuser). The
// one place that does need one is deleting a task with its attempts, and it says
// so at the call site.

// Task statuses, as stored in queue_tasks.status.
//
// There is deliberately no "failed": a failure that will be retried goes back to
// StatusPending with run_at pushed out, so the only terminal failure is
// StatusDead. See the migration's comment for why the name matters.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusDead      = "dead"
	StatusCancelled = "cancelled"
)

// Attempt outcomes, as stored in queue_attempts.outcome.
//
// OutcomeLost is what the reaper writes for an attempt whose worker vanished; it
// is the only outcome no worker ever reports about itself.
const (
	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
	OutcomeTimeout   = "timeout"
	OutcomeLost      = "lost"
	OutcomeCancelled = "cancelled"
)

// terminalStatuses are the statuses a task never leaves on its own. The pruner
// only ever deletes rows in one of these, and the admin retry action only accepts
// a task in one of them.
var terminalStatuses = []string{StatusSucceeded, StatusDead, StatusCancelled}

// Column-length ceilings. Both columns are TEXT on every dialect, so these are
// not schema limits — they are limits on what is worth storing. A Go panic's
// stack is a few kilobytes; a handler that returns a megabyte of error text
// should not turn one bad task into a bloated table.
const (
	maxLastErrorBytes  = 2000
	maxAttemptErrBytes = 8192
)

// enqueueParams is one row to insert. Every column is bound explicitly because no
// TEXT column in this schema carries a DEFAULT — which is itself what keeps the
// MySQL twin free of a length ceiling the others lack.
type enqueueParams struct {
	Kind        string
	Payload     []byte
	RunAt       int64 // UnixNano; the earliest this may run
	Priority    int
	MaxAttempts int
	TimeoutMS   int
	// ScheduleID is the plan that produced this task, or 0 for a direct enqueue.
	// Zero is stored as SQL NULL.
	ScheduleID int64
	// UniqueKey deduplicates; empty means "do not deduplicate", stored as SQL
	// NULL. It must be NULL and not "": all three dialects allow many NULLs in a
	// unique index and none allow many empty strings, so binding "" would make
	// the second un-keyed task collide with the first.
	UniqueKey string

	// delay is option plumbing, not a column: the After option cannot compute a
	// run_at without the service clock, so it records the offset here and Enqueue
	// resolves it. insertTask never reads this field.
	delay time.Duration
}

// insertTaskSQL is shared by both branches of insertTaskStmt; only the trailing
// RETURNING differs.
const insertTaskSQL = `INSERT INTO queue_tasks
   (kind, payload, status, priority, run_at, attempts, max_attempts, timeout_ms,
    lease_until, lease_token, worker, schedule_id, unique_key, last_error,
    created_at, updated_at, started_at, finished_at)
 VALUES (?, ?, ?, ?, ?, 0, ?, ?, 0, '', '', ?, ?, '', ?, ?, 0, 0)`

// insertTaskStmt returns the INSERT for a flavor, and whether it yields the new id
// as a row rather than through LastInsertId.
//
// PostgreSQL is the only one that asks for RETURNING, and it is also the only one
// that needs it: its driver has no LastInsertId. SQLite would accept the clause
// (3.35 and later) and MySQL has never had it, so asking only where the answer is
// otherwise unobtainable keeps the branch to the one dialect that forces it.
func insertTaskStmt(flavor sqldb.Flavor) (stmt string, returnsID bool) {
	if flavor == sqldb.PostgreSQL {
		return insertTaskSQL + ` RETURNING id`, true
	}
	return insertTaskSQL, false
}

// insertTask writes a pending task and returns its id.
//
// The id is not decoration: TriggerSchedule hands it to the admin area, which
// links to the task it just created, and setScheduleLastTask stores it as the
// plan's last_task_id. So the one dialect branch here is unavoidable —
// PostgreSQL's driver has no LastInsertId and only PostgreSQL is guaranteed to
// accept RETURNING (SQLite gained it in 3.35, MySQL has never had it). Reporting
// zero instead, as this once did, made every PostgreSQL enqueue look like a task
// that was never created.
//
// A duplicate unique_key surfaces as the driver's own error. It is not classified
// here on purpose: telling a unique violation from any other failure means
// matching driver-specific messages or codes in three drivers, and nothing in this
// package needs the distinction — the cron producer arbitrates with a CAS on
// queue_schedules instead (see fireDueSchedules), so the unique index is a
// backstop rather than a control flow.
func insertTask(ctx context.Context, db *sqldb.DB, p enqueueParams, now int64) (int64, error) {
	args := []any{
		p.Kind, string(p.Payload), StatusPending, p.Priority, p.RunAt,
		p.MaxAttempts, p.TimeoutMS,
		nullableID(p.ScheduleID), nullableText(p.UniqueKey),
		now, now,
	}

	stmt, returnsID := insertTaskStmt(db.Flavor)
	if returnsID {
		var id int64
		if err := db.QueryRowContext(ctx, stmt, args...).Scan(&id); err != nil {
			return 0, fmt.Errorf("queue: enqueue %s: %w", p.Kind, err)
		}
		return id, nil
	}

	res, err := db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, fmt.Errorf("queue: enqueue %s: %w", p.Kind, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("queue: enqueue %s: last insert id: %w", p.Kind, err)
	}
	return id, nil
}

// claimedTask is a task this process now owns, with the fencing token that proves
// it.
type claimedTask struct {
	ID          int64
	Kind        string
	Payload     []byte
	Attempt     int // already incremented, so this is the number of THIS attempt
	MaxAttempts int
	TimeoutMS   int
	ScheduleID  int64
	LeaseToken  string
	StartedAt   int64
}

// claimTasks takes up to limit due tasks for worker, in two phases.
//
// Phase one selects candidates with no lock; phase two takes each with an
// UPDATE whose WHERE still says status = 'pending'. That predicate is the
// compare-and-swap: all three engines take a write lock on the row and evaluate
// the predicate against committed data, so among concurrent claimers exactly one
// sees 'pending' and changes it. RowsAffected == 0 means someone else won — not an
// error, just the next candidate.
//
// No task can be lost. A task leaves 'pending' only by being claimed, by being
// cancelled from the admin area, or by being pruned (which only touches terminal
// rows); and a claimed task returns to 'pending' when its lease expires. So every
// task is always exactly one of: terminal, running under a live lease, or pending.
//
// attempts is incremented HERE, not at completion. That single choice is the whole
// mechanism by which a task that kills the process still burns an attempt and
// eventually lands in dead, instead of retrying forever and taking a worker down
// each time.
//
// kinds filters to what this process can actually run, which is what makes a
// rolling deployment safe: an old worker never claims a new kind, so it cannot
// fail it or consume its attempts. An empty kinds sends no query at all — a
// registry with no handlers has nothing to claim.
func claimTasks(
	ctx context.Context,
	db *sqldb.DB,
	kinds []string,
	limit int,
	now, leaseUntil int64,
	worker string,
) ([]claimedTask, error) {
	if len(kinds) == 0 || limit <= 0 {
		return nil, nil
	}

	args := make([]any, 0, len(kinds)+2)
	args = append(args, StatusPending, now)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, limit)

	rows, err := db.QueryContext(ctx,
		`SELECT id, kind, payload, attempts, max_attempts, timeout_ms, schedule_id
		   FROM queue_tasks
		  WHERE status = ? AND run_at <= ? AND kind IN (`+placeholders(len(kinds))+`)
		  ORDER BY priority DESC, run_at ASC, id ASC
		  LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("queue: select claim candidates: %w", err)
	}

	type candidate struct {
		id                             int64
		kind, payload                  string
		attempts, maxAttempts, timeout int
		scheduleID                     sql.NullInt64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.kind, &c.payload, &c.attempts, &c.maxAttempts,
			&c.timeout, &c.scheduleID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("queue: scan claim candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("queue: select claim candidates: %w", err)
	}
	// Closed before the UPDATEs below rather than deferred: with
	// MaxOpenConns(1) — which the test suite uses — holding the rows open would
	// deadlock against the first UPDATE's need for a connection.
	rows.Close()

	claimed := make([]claimedTask, 0, len(candidates))
	for _, c := range candidates {
		token, err := newLeaseToken()
		if err != nil {
			return claimed, err
		}
		res, err := db.ExecContext(ctx,
			`UPDATE queue_tasks
			    SET status = ?, attempts = attempts + 1, lease_until = ?, lease_token = ?,
			        worker = ?, started_at = ?, updated_at = ?
			  WHERE id = ? AND status = ?`,
			StatusRunning, leaseUntil, token, worker, now, now, c.id, StatusPending)
		if err != nil {
			return claimed, fmt.Errorf("queue: claim task %d: %w", c.id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return claimed, fmt.Errorf("queue: claim task %d: %w", c.id, err)
		}
		if n == 0 {
			continue // another worker took it between the two phases
		}
		claimed = append(claimed, claimedTask{
			ID:          c.id,
			Kind:        c.kind,
			Payload:     []byte(c.payload),
			Attempt:     c.attempts + 1,
			MaxAttempts: c.maxAttempts,
			TimeoutMS:   c.timeout,
			ScheduleID:  c.scheduleID.Int64,
			LeaseToken:  token,
			StartedAt:   now,
		})
	}
	return claimed, nil
}

// finishTask moves a running task to a terminal status, but only if this process
// still holds the lease.
//
// The lease_token predicate is the fencing check, and it is the last line of
// defence against duplicate execution. A process that stalled long enough for the
// reaper to reclaim its task — a long GC pause, a SIGSTOP — wakes up and tries to
// report success for work another worker may now be redoing. Reported as
// held=false so the caller discards its result and logs, rather than overwriting
// the row.
func finishTask(
	ctx context.Context, db *sqldb.DB,
	id int64, token, status, lastErr string, now int64,
) (held bool, err error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, last_error = ?, finished_at = ?, updated_at = ?,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status = ? AND lease_token = ?`,
		status, truncate(lastErr, maxLastErrorBytes), now, now,
		id, StatusRunning, token)
	if err != nil {
		return false, fmt.Errorf("queue: finish task %d: %w", id, err)
	}
	return rowsAffected(res, "finish task", id)
}

// retryTask sends a running task back to pending for another go, fenced the same
// way as finishTask.
//
// attempts is left alone: it was incremented at claim time and this is the same
// attempt's aftermath, not a new one.
func retryTask(
	ctx context.Context, db *sqldb.DB,
	id int64, token string, runAt int64, lastErr string, now int64,
) (held bool, err error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, run_at = ?, last_error = ?, updated_at = ?,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status = ? AND lease_token = ?`,
		StatusPending, runAt, truncate(lastErr, maxLastErrorBytes), now,
		id, StatusRunning, token)
	if err != nil {
		return false, fmt.Errorf("queue: retry task %d: %w", id, err)
	}
	return rowsAffected(res, "retry task", id)
}

// releaseTask hands a running task straight back, refunding the attempt.
//
// Used only when this process is shutting down and cancelled the handler itself.
// The refund is what separates it from the reaper's path: this is a known,
// deliberate interruption by an operator who is about to start a replacement
// process that can pick the task up immediately, so spending a retry on their
// deployment is spending it on nothing. The reaper cannot tell a deployment from a
// crash, so it never refunds.
//
// attempts is floored at zero rather than trusted to be positive: the column is
// read back from the database, and a delay computed from a corrupt row should still
// be sane.
func releaseTask(
	ctx context.Context, db *sqldb.DB,
	id int64, token string, runAt int64, lastErr string, now int64,
) (held bool, err error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, run_at = ?, last_error = ?, updated_at = ?,
		        attempts = CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status = ? AND lease_token = ?`,
		StatusPending, runAt, truncate(lastErr, maxLastErrorBytes), now,
		id, StatusRunning, token)
	if err != nil {
		return false, fmt.Errorf("queue: release task %d: %w", id, err)
	}
	return rowsAffected(res, "release task", id)
}

// attemptRow is one finished attempt, for the failure log.
type attemptRow struct {
	TaskID     int64
	Attempt    int
	Outcome    string
	Worker     string
	Error      string
	StartedAt  int64
	FinishedAt int64
}

// insertAttempt appends to the failure log.
//
// Callers must write the authoritative status change FIRST and only insert this
// once the fenced UPDATE reported held=true. The reverse order leaves a
// convincing log line for an attempt whose result was discarded; this order can
// only ever lose a log line, never duplicate an execution. It is also why the two
// are not wrapped in a transaction — one lost log row is a better failure than a
// transaction on the completion path.
func insertAttempt(ctx context.Context, db *sqldb.DB, a attemptRow) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO queue_attempts
		   (task_id, attempt, outcome, worker, error, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.TaskID, a.Attempt, a.Outcome, a.Worker,
		truncate(a.Error, maxAttemptErrBytes), a.StartedAt, a.FinishedAt)
	if err != nil {
		return fmt.Errorf("queue: record attempt %d of task %d: %w", a.Attempt, a.TaskID, err)
	}
	return nil
}

// leaseHold names one EXECUTION: a task id together with the fencing token that
// proves who is running it.
//
// The token is not redundant with the id. One process can legitimately hold two
// executions of the same task — its heartbeats fail through a database blip, another
// instance's reaper returns the task to pending, and this process's poller claims it
// again before its own heartbeat has noticed the loss. Bookkeeping keyed on the id
// alone cannot tell those two apart, and confusing them cancels the wrong handler.
type leaseHold struct {
	ID    int64
	Token string
}

// renewLeases extends the lease of every task this worker still holds, in one
// statement, and returns the holds it did NOT renew.
//
// Batched on worker rather than per-task so a heartbeat tick costs one statement
// instead of one per in-flight task. Matching on worker is safe because a worker id
// carries a per-process random suffix, so a zombie process and a live one never
// share one.
//
// The read-back compares lease_token, though, not just the id: a task this process
// has claimed TWICE is held under the second token, and the first execution has to
// be reported lost even though the row's id is still ours. Matching on the id alone
// would renew the stale execution forever.
//
// The holds that come back are the cooperative-cancellation signal, obtained for
// free: an execution missing from the renewal is one the reaper reclaimed, an
// operator cancelled, or this process superseded, so its handler's context should be
// cancelled. That is the entire implementation of "cancel a running task" — no extra
// column, no polling.
func renewLeases(
	ctx context.Context, db *sqldb.DB,
	holds []leaseHold, worker string, leaseUntil, now int64,
) (lost []leaseHold, err error) {
	if len(holds) == 0 {
		return nil, nil
	}

	// Deduplicated: two holds of one task differ only by token, and the row is the
	// same row to renew and to read back.
	ids := make([]int64, 0, len(holds))
	seen := make(map[int64]bool, len(holds))
	for _, h := range holds {
		if !seen[h.ID] {
			seen[h.ID] = true
			ids = append(ids, h.ID)
		}
	}

	args := make([]any, 0, len(ids)+4)
	args = append(args, leaseUntil, now, StatusRunning, worker)
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_tasks SET lease_until = ?, updated_at = ?
		  WHERE status = ? AND worker = ? AND id IN (`+placeholders(len(ids))+`)`,
		args...); err != nil {
		return nil, fmt.Errorf("queue: renew leases: %w", err)
	}

	// Which ones did we keep? RowsAffected cannot answer this — it is a count, and
	// MySQL does not even count a row whose values did not change — so the held set
	// is read back explicitly.
	sel := make([]any, 0, len(ids)+2)
	sel = append(sel, StatusRunning, worker)
	for _, id := range ids {
		sel = append(sel, id)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, lease_token FROM queue_tasks
		  WHERE status = ? AND worker = ? AND id IN (`+placeholders(len(ids))+`)`,
		sel...)
	if err != nil {
		return nil, fmt.Errorf("queue: read back renewed leases: %w", err)
	}
	defer rows.Close()

	held := make(map[leaseHold]bool, len(ids))
	for rows.Next() {
		var h leaseHold
		if err := rows.Scan(&h.ID, &h.Token); err != nil {
			return nil, fmt.Errorf("queue: read back renewed leases: %w", err)
		}
		held[h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: read back renewed leases: %w", err)
	}
	for _, h := range holds {
		if !held[h] {
			lost = append(lost, h)
		}
	}
	return lost, nil
}

// expiredLease is a running task whose worker has gone quiet.
type expiredLease struct {
	ID          int64
	Attempts    int
	MaxAttempts int
	Worker      string
	StartedAt   int64
	LeaseToken  string
	// LeaseUntil is when the lease ran out, which is the best available answer to
	// "when did this attempt stop?". The reaper only notices some time later, on its
	// next tick, and dating the attempt from the noticing would report a duration
	// that includes however long the queue took to look.
	LeaseUntil int64
}

// expiredLeases finds running tasks whose lease has run out.
func expiredLeases(ctx context.Context, db *sqldb.DB, now int64, limit int) ([]expiredLease, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, attempts, max_attempts, worker, started_at, lease_token, lease_until
		   FROM queue_tasks
		  WHERE status = ? AND lease_until > 0 AND lease_until <= ?
		  ORDER BY lease_until ASC
		  LIMIT ?`, StatusRunning, now, limit)
	if err != nil {
		return nil, fmt.Errorf("queue: select expired leases: %w", err)
	}
	defer rows.Close()

	out := []expiredLease{}
	for rows.Next() {
		var e expiredLease
		if err := rows.Scan(&e.ID, &e.Attempts, &e.MaxAttempts, &e.Worker,
			&e.StartedAt, &e.LeaseToken, &e.LeaseUntil); err != nil {
			return nil, fmt.Errorf("queue: scan expired lease: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: select expired leases: %w", err)
	}
	return out, nil
}

// taskCell is one (status, kind) pair, with how many rows of queue_tasks are in it.
type taskCell struct {
	Status string
	Kind   string
	Count  int
}

// taskCounts counts the whole table once, grouped by status and kind.
//
// One statement in place of three. The list screen needs the total for its filter, a
// count per status for the summary line, and the pending count per unhandled kind for
// the orphan banner — and every one of those is a sum over this grid. Asking them
// separately meant three aggregates over the same table on every page view, and every
// operator action redirects back to the list, so a retry or a cancel paid for them
// again.
//
// The result stays small however large the table gets: five statuses times the
// distinct kinds ever enqueued, which is bounded by the handlers a project registers
// plus whatever it has since removed.
func taskCounts(ctx context.Context, db *sqldb.DB) ([]taskCell, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT status, kind, COUNT(*) FROM queue_tasks GROUP BY status, kind`)
	if err != nil {
		return nil, fmt.Errorf("queue: count tasks: %w", err)
	}
	defer rows.Close()

	out := []taskCell{}
	for rows.Next() {
		var c taskCell
		if err := rows.Scan(&c.Status, &c.Kind, &c.Count); err != nil {
			return nil, fmt.Errorf("queue: count tasks: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: count tasks: %w", err)
	}
	return out, nil
}

// orphanCounts reports how many pending tasks exist per kind that this process
// has no handler for.
//
// Such tasks are never claimed (see claimTasks), so without this they would grow
// in silence. The scheduler logs it and the admin list surfaces it: stopped but
// visible is better than either failing them or retrying them forever.
//
// An empty kinds means every pending task is an orphan, which is the correct
// answer for a process with no handlers at all.
//
// This asks only about pending rows, so the status index answers it without reading
// the terminal rows that make up the bulk of the table — which is why the scheduler's
// tick and the CLI use it rather than deriving the same numbers from taskCounts. The
// list screen goes the other way for the same reason: it has already paid for the
// whole grid, and asking again would be a second scan for numbers in hand.
func orphanCounts(ctx context.Context, db *sqldb.DB, kinds []string) (map[string]int, error) {
	q := `SELECT kind, COUNT(*) FROM queue_tasks WHERE status = ?`
	args := []any{StatusPending}
	if len(kinds) > 0 {
		q += ` AND kind NOT IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` GROUP BY kind`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("queue: count orphan kinds: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, fmt.Errorf("queue: count orphan kinds: %w", err)
		}
		out[kind] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: count orphan kinds: %w", err)
	}
	return out, nil
}

// orphanTask is a pending task with no handler, and the kind that explains why.
type orphanTask struct {
	ID   int64
	Kind string
}

// orphanTasks finds pending tasks older than cutoff whose kind has no handler.
//
// The kind comes back with the id so the caller can name it in the failure log
// without a second query per task. Killed one at a time rather than by a single
// sweeping UPDATE for the same reason: the log gets a row per task, and an orphan
// declared dead with nothing in its log is a task that stopped for no visible reason.
func orphanTasks(ctx context.Context, db *sqldb.DB, kinds []string, cutoff int64, limit int) ([]orphanTask, error) {
	q := `SELECT id, kind FROM queue_tasks WHERE status = ? AND created_at < ?`
	args := []any{StatusPending, cutoff}
	if len(kinds) > 0 {
		q += ` AND kind NOT IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("queue: select orphan tasks: %w", err)
	}
	defer rows.Close()

	out := []orphanTask{}
	for rows.Next() {
		var o orphanTask
		if err := rows.Scan(&o.ID, &o.Kind); err != nil {
			return nil, fmt.Errorf("queue: scan orphan task: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: select orphan tasks: %w", err)
	}
	return out, nil
}

// killPending moves a pending task to dead. Used for orphans; fenced on the
// status alone, since a pending task has no lease to hold.
func killPending(
	ctx context.Context, db *sqldb.DB, id int64, lastErr string, now int64,
) (changed bool, err error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, last_error = ?, finished_at = ?, updated_at = ?
		  WHERE id = ? AND status = ?`,
		StatusDead, truncate(lastErr, maxLastErrorBytes), now, now, id, StatusPending)
	if err != nil {
		return false, fmt.Errorf("queue: kill pending task %d: %w", id, err)
	}
	return rowsAffected(res, "kill pending task", id)
}

// prunableIDs finds terminal tasks that finished before cutoff.
//
// A separate read rather than a DELETE with a subquery: MySQL refuses
// `LIMIT` inside an `IN (SELECT ...)` subquery, so an unbounded delete would be the
// only portable single-statement form — and an unbounded delete on this table is
// how a prune tick turns into a lock-up. Same reasoning as counting permissions in
// Go rather than with json_array_length in group_crud.go: two round trips beat a
// dialect branch.
func prunableIDs(ctx context.Context, db *sqldb.DB, status string, cutoff int64, limit int) ([]int64, error) {
	return scanIDs(ctx, db,
		`SELECT id FROM queue_tasks
		  WHERE status = ? AND finished_at > 0 AND finished_at < ?
		  ORDER BY id ASC LIMIT ?`,
		[]any{status, cutoff, limit}, "select prunable tasks")
}

// deleteTasks removes tasks and their attempts.
//
// Attempts first, so an interruption leaves orphaned log rows (which the pruner's
// own sweep collects) rather than tasks whose history has silently vanished.
func deleteTasks(ctx context.Context, db *sqldb.DB, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := placeholders(len(ids))

	if _, err := db.ExecContext(ctx,
		`DELETE FROM queue_attempts WHERE task_id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("queue: delete attempts: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM queue_tasks WHERE id IN (`+ph+`)`, args...); err != nil {
		return fmt.Errorf("queue: delete tasks: %w", err)
	}
	return nil
}

// deleteOrphanAttempts removes attempt rows whose task is gone.
//
// The `NOT IN (SELECT id FROM queue_tasks)` shape is expensive on a large table,
// which is why the caller runs it rarely and with a small limit. It exists because
// the admin delete action and deleteTasks both write attempts and tasks as two
// statements, so an interruption between them is possible by design.
func deleteOrphanAttempts(ctx context.Context, db *sqldb.DB, limit int) (int64, error) {
	ids, err := scanIDs(ctx, db,
		`SELECT id FROM queue_attempts
		  WHERE task_id NOT IN (SELECT id FROM queue_tasks)
		  ORDER BY id ASC LIMIT ?`,
		[]any{limit}, "select orphan attempts")
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM queue_attempts WHERE id IN (`+placeholders(len(ids))+`)`, args...); err != nil {
		return 0, fmt.Errorf("queue: delete orphan attempts: %w", err)
	}
	return int64(len(ids)), nil
}

// --- small shared helpers ---

// placeholders returns "?, ?, ?" for n. The only string built into a statement in
// this package, and its content depends on nothing but a Go slice's length — there
// is no path by which caller data reaches it.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func scanIDs(ctx context.Context, db *sqldb.DB, q string, args []any, what string) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("queue: %s: %w", what, err)
	}
	defer rows.Close()

	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("queue: %s: %w", what, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: %s: %w", what, err)
	}
	return out, nil
}

func rowsAffected(res sql.Result, what string, id int64) (bool, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("queue: %s %d: %w", what, id, err)
	}
	return n > 0, nil
}

// nullableID maps 0 to SQL NULL, so an absent schedule_id is NULL rather than a
// reference to a schedule with id 0.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// nullableText maps "" to SQL NULL. Required for unique_key: every dialect allows
// many NULLs in a unique index and none allows many empty strings, so binding ""
// would make the second un-keyed row collide with the first.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// truncate caps a string at n bytes without splitting a rune, appending an ellipsis
// so a reader can tell it was cut. Cutting mid-rune would store invalid UTF-8,
// which PostgreSQL rejects outright on a text column.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const suffix = "…"
	cut := n - len(suffix)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// newLeaseToken returns a fresh fencing token.
//
// crypto/rand, not math/rand: the token's whole job is to be unguessable-by-
// accident across processes and restarts, and a math/rand source seeded per
// process is exactly the kind of thing that ends up identical in two containers
// started from the same image at the same instant. 16 bytes hex-encoded is 32
// characters, matching the MySQL column.
func newLeaseToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("queue: generate lease token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// errNoRowsForID is returned where a caller asked about a specific row that is
// gone. Kept distinct from a database failure so the HTTP layer answers 404 for one
// and 500 for the other — the same split findGroupRow makes.
var errNoRowsForID = errors.New("queue: no such row")
