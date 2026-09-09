package queue

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
)

// Cron plans: keeping the database in step with the code, and firing what is due.
//
// The requirement these two halves implement together is "the code is the source of
// truth, but an operator may change a schedule from the admin area". Those pull in
// opposite directions, and the whole resolution is one extra column.

// maxSyncRetries bounds the optimistic retry in syncOne. Three is plenty: the only
// thing that invalidates the read is another instance syncing the same plan at the
// same moment, which happens at most once per deployment.
const maxSyncRetries = 3

// scheduleRow is one plan as stored.
type scheduleRow struct {
	ID          int64
	Name        string
	Kind        string
	Payload     string
	Spec        string
	CodeSpec    string
	Enabled     bool
	Present     bool
	NextRunAt   int64
	LastFireAt  int64
	LastTaskID  int64
	MaxAttempts int
}

// Drifted reports that an operator has changed the expression from what the code
// declares. Derived rather than stored: spec != code_spec IS the drift, so there is
// no flag to keep in step with it.
func (r scheduleRow) Drifted() bool { return r.Spec != r.CodeSpec }

// syncSchedules reconciles the registry's plans with queue_schedules.
//
// Called from Start, before any goroutine exists, so a plan is never fired against
// a half-synced row.
//
// It is skipped entirely for a registry that is not complete. Both halves of this
// function would otherwise be destructive: a process holding a subset of the
// application's plans would mark everything else present=0, hiding work it simply
// cannot see. There is no way to build an incomplete registry today — the field
// exists so that adding one (a `queue worker --kinds=...` flag, say) cannot happen
// without meeting this comment.
func (s *Service) syncSchedules(ctx context.Context) error {
	if !s.reg.complete {
		s.log.Warn("queue: registry is not complete; skipping schedule sync")
		return nil
	}

	entries := s.reg.cronEntries()
	for _, e := range entries {
		if err := s.syncOne(ctx, e); err != nil {
			return err
		}
	}
	return s.markAbsentSchedules(ctx, entries)
}

// syncOne is the three-way merge for a single plan.
//
//	base   = the stored code_spec (what the code said at the last sync)
//	ours   = the stored spec      (what is in effect now)
//	theirs = the registered spec  (what the code says now)
//
// If ours == base nobody touched it, so it follows theirs. If they differ an
// operator edited it, so ours is kept and only base moves forward — which is what
// lets the admin screen say "code default X, currently Y" and offer to reset.
//
// Read-then-conditionally-write, in two statements, rather than one clever UPDATE
// with a CASE. A single statement would have to reference the pre-update value of
// spec while assigning it, and MySQL documents `SET a = 1, b = a` as using the NEW
// a while the other two use the old one — the same statement would mean different
// things on different dialects. The read values become the UPDATE's optimistic
// predicate, so a concurrent sync by another instance is detected and retried
// rather than silently overwritten.
func (s *Service) syncOne(ctx context.Context, e cronEntry) error {
	for attempt := range maxSyncRetries {
		stored, err := findScheduleByName(ctx, s.db, e.name)
		switch {
		case errors.Is(err, errNoRowsForID):
			next, nerr := e.schedule.Next(s.now().In(s.Location()))
			if nerr != nil {
				return fmt.Errorf("queue: cron %q: %w", e.name, nerr)
			}
			if err := insertSchedule(ctx, s.db, e, next.UnixNano(), s.now().UnixNano()); err != nil {
				// Another instance inserted the same name between the read and the
				// insert. The unique index on name caught it; loop round and take the
				// update path instead of trying to classify the driver's error.
				if attempt < maxSyncRetries-1 {
					continue
				}
				return err
			}
			return nil
		case err != nil:
			return err
		}

		// Compute everything before writing, so the write is one statement whose
		// predicate is exactly what was read.
		spec := stored.Spec
		nextRunAt := stored.NextRunAt
		if stored.Spec == stored.CodeSpec && stored.Spec != e.spec {
			// Untouched by an operator, and the code changed: follow the code, and
			// recompute the next fire because the old one belongs to the old
			// expression.
			spec = e.spec
			next, nerr := e.schedule.Next(s.now().In(s.Location()))
			if nerr != nil {
				return fmt.Errorf("queue: cron %q: %w", e.name, nerr)
			}
			nextRunAt = next.UnixNano()
		}
		// next_run_at is otherwise left exactly as it was. Recomputing it on every
		// start would shift a daily plan forward a little at each deployment, and
		// recomputing from now would let a restart loop starve it entirely.

		ok, err := updateScheduleFromCode(ctx, s.db, stored, e, spec, nextRunAt, s.now().UnixNano())
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		// Another instance changed the row between the read and the write. Re-read.
	}
	return fmt.Errorf("queue: cron %q: schedule sync kept losing to a concurrent writer", e.name)
}

// markAbsentSchedules flags plans the code no longer declares.
//
// present=0 rather than DELETE. Deleting would throw away an operator's expression
// edit and the plan's enabled state because of one deployment that happened to be
// missing a handler, and would orphan the schedule_id of every task the plan ever
// produced. A flagged row is never fired; the admin screen shows it as removed from
// the code and offers the only destructive action that makes sense there.
func (s *Service) markAbsentSchedules(ctx context.Context, entries []cronEntry) error {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}

	q := `UPDATE queue_schedules SET present = 0, updated_at = ? WHERE present = 1`
	args := []any{s.now().UnixNano()}
	if len(names) > 0 {
		q += ` AND name NOT IN (` + placeholders(len(names)) + `)`
		for _, n := range names {
			args = append(args, n)
		}
	}
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("queue: mark absent schedules: %w", err)
	}
	return nil
}

// fireDueSchedules enqueues a task for every plan that has come due, at most once
// per firing across every process.
//
// The arbitration is a compare-and-swap on next_run_at, and the order matters: the
// CAS comes FIRST and only its winner inserts a task. next_run_at moves strictly
// forward, so the value just read is a fencing token — an instance whose UPDATE
// affects no rows knows another instance already claimed this firing and skips the
// row entirely.
//
// This is why the design does not lean on the unique index for cron. Classifying a
// unique violation means matching driver-specific errors in three drivers; a CAS on
// a column needs nothing but RowsAffected. The unique_key written below is still
// carried as a cheap second line of defence — if an operator edits the expression
// and resets next_run_at to a value already used, it prevents the duplicate — but
// nothing depends on detecting its failure.
//
// The cost of CAS-then-insert is a process dying between the two: that firing is
// skipped and the next one happens normally. Accepted deliberately. Cron is not
// exactly-once across a crash at that instant, and the alternative ordering
// (insert, then CAS) trades a rare skipped run for a rare doubled one, which is the
// worse direction for a job that sends mail or bills someone.
//
// Missed firings are never made up. A process that was down for three days finds a
// daily plan's next_run_at far in the past and fires it ONCE, because the new value
// is computed from now rather than by advancing the old one. Catch-up would dump
// three identical maintenance jobs into the queue the moment service returned.
func (s *Service) fireDueSchedules(ctx context.Context) error {
	now := s.now()
	due, err := dueSchedules(ctx, s.db, now.UnixNano())
	if err != nil {
		return err
	}

	for _, row := range due {
		sched, err := s.ParseCron(row.Spec)
		if err != nil {
			// An expression the admin form accepted cannot get here, but a row
			// edited by hand can. Logged and skipped rather than returned: one bad
			// plan must not stop the others from firing.
			s.log.Error("queue: schedule has an unparseable expression",
				"schedule", row.Name, "spec", row.Spec, "err", err)
			continue
		}
		if _, ok := s.reg.lookup(row.Kind); !ok {
			// Registered as a plan but not as a handler. Start refuses this for the
			// code's own plans, so it means the row's kind was edited by hand.
			s.log.Warn("queue: schedule fires a kind with no handler here",
				"schedule", row.Name, "kind", row.Kind)
			continue
		}

		next, err := sched.Next(now.In(s.Location()))
		if err != nil {
			s.log.Error("queue: schedule has no future firing",
				"schedule", row.Name, "spec", row.Spec, "err", err)
			continue
		}

		won, err := claimScheduleFiring(ctx, s.db, row.ID, row.NextRunAt,
			next.UnixNano(), now.UnixNano())
		if err != nil {
			return err
		}
		if !won {
			continue // another instance fired this one
		}

		// The plan's own ceiling wins over the kind's, since a row can carry an
		// operator's edit; everything else (priority, timeout) comes from the
		// registration through newTaskParams.
		p := s.newTaskParams(row.Kind, []byte(row.Payload), now.UnixNano())
		p.MaxAttempts = cmp.Or(row.MaxAttempts, p.MaxAttempts)
		p.ScheduleID = row.ID
		p.UniqueKey = fmt.Sprintf("cron:%d:%d", row.ID, row.NextRunAt)
		id, err := insertTask(ctx, s.db, p, now.UnixNano())
		if err != nil {
			// The firing is already claimed, so this plan will not be retried until
			// its next occurrence. Logged loudly rather than returned: the remaining
			// plans should still fire.
			s.log.Error("queue: could not enqueue a scheduled task",
				"schedule", row.Name, "kind", row.Kind, "err", err)
			continue
		}
		if err := setScheduleLastTask(ctx, s.db, row.ID, id, now.UnixNano()); err != nil {
			s.log.Error("queue: could not record a schedule's last task",
				"schedule", row.Name, "task", id, "err", err)
		}
		s.nudge()
		s.log.Debug("queue: fired schedule", "schedule", row.Name, "task", id)
	}
	return nil
}

// --- statements ---

func findScheduleByName(ctx context.Context, db *sqldb.DB, name string) (scheduleRow, error) {
	var r scheduleRow
	var enabled, present int
	err := db.QueryRowContext(ctx,
		`SELECT id, name, kind, payload, spec, code_spec, enabled, present,
		        next_run_at, last_fire_at, last_task_id, max_attempts
		   FROM queue_schedules WHERE name = ?`, name).
		Scan(&r.ID, &r.Name, &r.Kind, &r.Payload, &r.Spec, &r.CodeSpec, &enabled, &present,
			&r.NextRunAt, &r.LastFireAt, &r.LastTaskID, &r.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return scheduleRow{}, errNoRowsForID
	}
	if err != nil {
		return scheduleRow{}, fmt.Errorf("queue: find schedule %q: %w", name, err)
	}
	r.Enabled, r.Present = enabled != 0, present != 0
	return r, nil
}

func insertSchedule(ctx context.Context, db *sqldb.DB, e cronEntry, nextRunAt, now int64) error {
	// enabled starts at 1 and sync never touches it again: it is the operator's
	// column from this moment on.
	_, err := db.ExecContext(ctx,
		`INSERT INTO queue_schedules
		   (name, kind, payload, spec, code_spec, enabled, present,
		    next_run_at, last_fire_at, last_task_id, max_attempts, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, 1, ?, 0, 0, ?, ?, ?)`,
		e.name, e.kind, string(e.payload), e.spec, e.spec,
		nextRunAt, e.maxAttempts, now, now)
	if err != nil {
		return fmt.Errorf("queue: insert schedule %q: %w", e.name, err)
	}
	return nil
}

// updateScheduleFromCode writes the merged row, using the values that were read as
// an optimistic predicate. Reports false when another writer got there first.
//
// enabled is absent from the SET list on purpose: it belongs to the operator, and
// a sync that reset it would re-enable a plan somebody deliberately paused every
// time the process restarted.
func updateScheduleFromCode(
	ctx context.Context, db *sqldb.DB,
	stored scheduleRow, e cronEntry,
	spec string, nextRunAt, now int64,
) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_schedules
		    SET kind = ?, payload = ?, code_spec = ?, spec = ?, next_run_at = ?,
		        max_attempts = ?, present = 1, updated_at = ?
		  WHERE id = ? AND spec = ? AND code_spec = ?`,
		e.kind, string(e.payload), e.spec, spec, nextRunAt,
		e.maxAttempts, now,
		stored.ID, stored.Spec, stored.CodeSpec)
	if err != nil {
		return false, fmt.Errorf("queue: sync schedule %q: %w", e.name, err)
	}
	return rowsAffected(res, "sync schedule", stored.ID)
}

// dueSchedules reads the plans that have come due. present=1 and enabled=1 are both
// part of the predicate: a plan the code dropped and a plan an operator paused are
// both "do not fire", for different reasons.
func dueSchedules(ctx context.Context, db *sqldb.DB, now int64) ([]scheduleRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, kind, payload, spec, code_spec, enabled, present,
		        next_run_at, last_fire_at, last_task_id, max_attempts
		   FROM queue_schedules
		  WHERE enabled = 1 AND present = 1 AND next_run_at > 0 AND next_run_at <= ?
		  ORDER BY next_run_at ASC, id ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("queue: select due schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []scheduleRow{}
	for rows.Next() {
		var r scheduleRow
		var enabled, present int
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Payload, &r.Spec, &r.CodeSpec,
			&enabled, &present, &r.NextRunAt, &r.LastFireAt, &r.LastTaskID,
			&r.MaxAttempts); err != nil {
			return nil, fmt.Errorf("queue: scan due schedule: %w", err)
		}
		r.Enabled, r.Present = enabled != 0, present != 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: select due schedules: %w", err)
	}
	return out, nil
}

// claimScheduleFiring advances next_run_at, but only from the value the caller read.
//
// This is the whole of "many instances, one firing". next_run_at only ever moves
// forward, so `WHERE next_run_at = <what I read>` is a fencing token that needs no
// lock table, no advisory lock and no error classification.
func claimScheduleFiring(
	ctx context.Context, db *sqldb.DB,
	id, witness, next, now int64,
) (won bool, err error) {
	res, err := db.ExecContext(ctx,
		`UPDATE queue_schedules
		    SET next_run_at = ?, last_fire_at = ?, updated_at = ?
		  WHERE id = ? AND next_run_at = ?`,
		next, now, now, id, witness)
	if err != nil {
		return false, fmt.Errorf("queue: claim schedule firing %d: %w", id, err)
	}
	return rowsAffected(res, "claim schedule firing", id)
}

// setScheduleLastTask records which task a firing produced, so the admin list can
// show the plan's most recent outcome with one join on a primary key instead of a
// per-row "latest task of this kind" query.
func setScheduleLastTask(ctx context.Context, db *sqldb.DB, scheduleID, taskID, now int64) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE queue_schedules SET last_task_id = ?, updated_at = ? WHERE id = ?`,
		taskID, now, scheduleID); err != nil {
		return fmt.Errorf("queue: set last task of schedule %d: %w", scheduleID, err)
	}
	return nil
}
