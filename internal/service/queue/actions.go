package queue

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
)

// Operator actions: retry, cancel, run now, delete, and the schedule edits.
//
// Every one of them expresses its precondition as a WHERE clause and reports refusal
// through RowsAffected, never as a SELECT followed by an UPDATE. Two statements is a
// race: a task claimed by a worker between the check and the write would be
// overwritten out from under it. This is the same shape groupDelete uses to refuse a
// group that still has members.
//
// So each returns (ok bool, err error): false means "the task is not in a state where
// this makes sense", which the caller turns into a flash message, and err means the
// database failed, which the caller turns into a 500.

// ErrScheduleAbsent is returned for an operation on a plan the code no longer
// declares, or one whose kind has no handler here. A refusal rather than a failure:
// there is nothing wrong with the request, the plan just cannot run.
var ErrScheduleAbsent = errors.New("queue: schedule is not runnable in this process")

// RetryTask puts a terminal task back in the queue.
//
// attempts is deliberately NOT reset. It is the audit trail, and the numbering in
// queue_attempts is derived from it — zeroing it would make the log show attempt 1
// twice and force the detail screen to number rows by position instead of by value.
// Instead the ceiling is raised, which is also the more accurate reading of what the
// operator asked for: give it another few goes.
//
// extra is how many further attempts to allow; values below 1 are treated as 1, since
// "retry" with no attempt available would be a no-op that looked like it worked.
func (s *Service) RetryTask(ctx context.Context, id int64, extra int) (bool, error) {
	if extra < 1 {
		extra = 1
	}
	now := s.now().UnixNano()
	res, err := s.db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, run_at = ?, last_error = '', finished_at = 0, updated_at = ?,
		        max_attempts = attempts + ?,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status IN (?, ?, ?)`,
		StatusPending, now, now, extra,
		id, StatusSucceeded, StatusDead, StatusCancelled)
	if err != nil {
		return false, fmt.Errorf("queue: retry task %d: %w", id, err)
	}
	return rowsAffected(res, "retry task", id)
}

// RunTaskNow brings a task's run_at forward so the next poll picks it up.
//
// It does NOT execute anything inline, and the wording the admin screen uses has to
// be honest about that. Running it inline would hold an HTTP request open for the
// handler's whole duration, run it in the web process rather than a worker, and bypass
// the lease — so a worker polling at the same moment would run it too.
//
// Accepted for a pending task with a future run_at (bringing a delayed task forward)
// and for a terminal one (the same thing as a retry, from a different button).
func (s *Service) RunTaskNow(ctx context.Context, id int64) (bool, error) {
	now := s.now().UnixNano()
	res, err := s.db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, run_at = ?, finished_at = 0, updated_at = ?,
		        max_attempts = CASE WHEN max_attempts <= attempts
		                            THEN attempts + 1 ELSE max_attempts END,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status IN (?, ?, ?, ?)`,
		StatusPending, now, now,
		id, StatusPending, StatusSucceeded, StatusDead, StatusCancelled)
	if err != nil {
		return false, fmt.Errorf("queue: run task %d now: %w", id, err)
	}
	return rowsAffected(res, "run task now", id)
}

// CancelTask marks a task cancelled, including one that is currently running.
//
// Cancelling a running task works without any new column or any polling by the
// handler: changing the status makes the owner's next heartbeat fail to renew that
// row, and the heartbeat cancels the handler's context for exactly the rows it could
// not renew. Fencing then refuses the handler's eventual result, so it cannot
// resurrect the row.
//
// The consequence is a delay of up to one heartbeat interval before the handler
// actually stops, and the admin screen has to say so rather than implying the work
// halted the moment the button was pressed.
func (s *Service) CancelTask(ctx context.Context, id int64) (bool, error) {
	now := s.now().UnixNano()
	res, err := s.db.ExecContext(ctx,
		`UPDATE queue_tasks
		    SET status = ?, finished_at = ?, updated_at = ?,
		        lease_until = 0, lease_token = '', worker = ''
		  WHERE id = ? AND status IN (?, ?)`,
		StatusCancelled, now, now, id, StatusPending, StatusRunning)
	if err != nil {
		return false, fmt.Errorf("queue: cancel task %d: %w", id, err)
	}
	return rowsAffected(res, "cancel task", id)
}

// DeleteTask removes a task and its attempts.
//
// Refused for a running task: deleting the row a worker holds would make its
// completion write fail in a way nothing is watching for, and the operator almost
// certainly meant to cancel it first.
//
// This is the one write path in the package that uses a transaction, because losing
// the second statement would leave attempt rows with no task. sqldb.Transaction begins
// with context.Background() so the transaction itself is not cancellable — hence the
// statements inside take ctx explicitly, and nothing here touches s.db while it is
// open (both noted on Admin.keepingASuperuser).
func (s *Service) DeleteTask(ctx context.Context, id int64) (bool, error) {
	var deleted bool
	err := s.db.Transaction(func(tx *sqldb.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM queue_tasks WHERE id = ? AND status != ?`, id, StatusRunning)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil // missing, or running: nothing deleted, and not an error
		}
		deleted = true
		_, err = tx.ExecContext(ctx, `DELETE FROM queue_attempts WHERE task_id = ?`, id)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("queue: delete task %d: %w", id, err)
	}
	return deleted, nil
}

// SetScheduleSpec changes a plan's live expression and recomputes its next firing.
//
// Recomputing is not optional. Without it the new expression takes effect only after
// the old next_run_at passes, which for a daily plan means "my change did nothing"
// for up to a day — and that is exactly the moment an operator concludes the screen
// is broken.
//
// The expression is validated here as well as in the form, through the same parser
// the scheduler runs on. A second implementation anywhere is how "saved fine, never
// fires" happens.
func (s *Service) SetScheduleSpec(ctx context.Context, id int64, expr string) (bool, error) {
	sched, err := s.ParseCron(expr)
	if err != nil {
		return false, err
	}
	next, err := sched.Next(s.now().In(s.Location()))
	if err != nil {
		return false, err
	}
	now := s.now().UnixNano()
	res, err := s.db.ExecContext(ctx,
		`UPDATE queue_schedules SET spec = ?, next_run_at = ?, updated_at = ? WHERE id = ?`,
		expr, next.UnixNano(), now, id)
	if err != nil {
		return false, fmt.Errorf("queue: set schedule %d spec: %w", id, err)
	}
	return rowsAffected(res, "set schedule spec", id)
}

// ResetScheduleSpec puts a plan back on the expression the code declares.
//
// The whole visible payoff of carrying code_spec: an operator who overrode an
// expression can undo it without knowing what the code said.
func (s *Service) ResetScheduleSpec(ctx context.Context, id int64) (bool, error) {
	row, err := s.Schedule(ctx, id)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	return s.SetScheduleSpec(ctx, id, row.CodeSpec)
}

// SetScheduleEnabled pauses or resumes a plan.
//
// enabled is taken from the caller rather than toggled from the stored value, because
// a form submitted twice must not flip it back — the same reason userSetStatus reads
// the value out of the request.
//
// Resuming recomputes next_run_at from now. A plan paused for a week would otherwise
// come back with a next firing far in the past and fire immediately on resume, which
// is not what "unpause" means.
func (s *Service) SetScheduleEnabled(ctx context.Context, id int64, enabled bool) (bool, error) {
	now := s.now().UnixNano()
	if !enabled {
		res, err := s.db.ExecContext(ctx,
			`UPDATE queue_schedules SET enabled = 0, updated_at = ? WHERE id = ?`, now, id)
		if err != nil {
			return false, fmt.Errorf("queue: pause schedule %d: %w", id, err)
		}
		return rowsAffected(res, "pause schedule", id)
	}

	row, err := s.Schedule(ctx, id)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	sched, err := s.ParseCron(row.Spec)
	if err != nil {
		return false, err
	}
	next, err := sched.Next(s.now().In(s.Location()))
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE queue_schedules SET enabled = 1, next_run_at = ?, updated_at = ? WHERE id = ?`,
		next.UnixNano(), now, id)
	if err != nil {
		return false, fmt.Errorf("queue: resume schedule %d: %w", id, err)
	}
	return rowsAffected(res, "resume schedule", id)
}

// TriggerSchedule enqueues one task from a plan, right now, and returns its id.
//
// next_run_at is deliberately left alone: firing by hand is an extra run, not a
// replacement for the scheduled one, and advancing it would silently skip a cycle.
//
// A paused plan may still be triggered. "Stop running this automatically" and "never
// run this again" are different requests, and verifying a plan by hand after pausing
// it is exactly what an operator does next.
//
// Refused with ErrScheduleAbsent when the plan is gone from the code or its kind has
// no handler here — in both cases the task would be enqueued and never claimed.
func (s *Service) TriggerSchedule(ctx context.Context, id int64) (int64, error) {
	row, err := s.Schedule(ctx, id)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, nil
	}
	if !row.Present || !row.KnownKind {
		return 0, fmt.Errorf("queue: schedule %q fires kind %q, which has no handler here: %w",
			row.Name, row.Kind, ErrScheduleAbsent)
	}

	// Built exactly as fireDueSchedules builds it, through the one helper that knows
	// a kind's defaults. Filling these in by hand is how the hand-fired task came to
	// differ from the automatic one: a plan registered without WithMaxAttempts stores
	// 0, and enqueueing that verbatim produced a task whose first failure was also its
	// last (finish sees attempt 1 >= 0 and marks it dead) — on the very path an
	// operator uses to verify a plan that has been failing.
	//
	// No unique_key. The cron producer keys on the firing instant to make a double fire
	// impossible; a manual trigger is a deliberate act, and keying it would refuse a
	// second press rather than doing what was asked.
	now := s.now().UnixNano()
	p := s.newTaskParams(row.Kind, []byte(row.Payload), now)
	p.MaxAttempts = cmp.Or(row.MaxAttempts, p.MaxAttempts)
	p.ScheduleID = row.ID

	taskID, err := insertTask(ctx, s.db, p, now)
	if err != nil {
		return 0, err
	}
	if err := setScheduleLastTask(ctx, s.db, row.ID, taskID, now); err != nil {
		return taskID, err
	}
	s.nudge()
	return taskID, nil
}

// DeleteSchedule removes a plan, but only one the code no longer declares.
//
// The guard is what makes "the code is the source of truth" real rather than
// advisory: deleting a live plan would achieve nothing, since the next startup syncs
// it straight back. The admin area therefore offers deletion only for a plan already
// flagged present=0, and this refuses it in any case.
func (s *Service) DeleteSchedule(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM queue_schedules WHERE id = ? AND present = 0`, id)
	if err != nil {
		return false, fmt.Errorf("queue: delete schedule %d: %w", id, err)
	}
	return rowsAffected(res, "delete schedule", id)
}
