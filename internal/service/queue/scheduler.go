package queue

import (
	"context"
	"fmt"
	"time"
)

// The periodic half of the worker: one ticker, four jobs.
//
// Cron firing, lease reaping, the orphan sweep and retention pruning all share one
// goroutine and one ticker. They are all "do this every so often, never overlapping
// with itself", and running them in sequence on one ticker gets that for free while
// also guaranteeing they never hit the database at the same moment. The worker pool
// is not reused for them: its loop is driven by free slots, theirs by a clock.
//
// Every job is a named method that takes only a context, so a test calls it
// directly. The loop below is a five-line shell with nothing in it worth testing —
// which is the point, and the reason no Clock interface or injectable ticker
// appears anywhere in this package.

// tickLoop drives the periodic jobs until ctx is cancelled.
func (s *Service) tickLoop(ctx context.Context) {
	ticker := time.NewTicker(s.TickInterval())
	defer ticker.Stop()

	// Pruning and the orphan sweep run at their own, much lower, frequency. Tracked
	// as a last-run stamp rather than a second ticker so the whole periodic surface
	// stays one goroutine.
	var lastPrune time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if err := s.fireDueSchedules(ctx); err != nil {
			s.log.Error("queue: firing due schedules failed", "err", err)
		}
		if err := s.reapOnce(ctx); err != nil {
			s.log.Error("queue: reaping expired leases failed", "err", err)
		}

		if now := s.now(); now.Sub(lastPrune) >= s.PruneInterval() {
			lastPrune = now
			if err := s.sweepOrphans(ctx); err != nil {
				s.log.Error("queue: orphan sweep failed", "err", err)
			}
			if err := s.pruneOnce(ctx); err != nil {
				s.log.Error("queue: prune failed", "err", err)
			}
		}
	}
}

// reapOnce returns tasks whose worker has gone quiet to the queue, or gives up on
// them if they are out of attempts.
//
// The lease is the only thing that distinguishes "being worked on" from "abandoned",
// so this is what makes a `kill -9` recoverable. Each row is taken with a
// compare-and-swap on its lease token, so several instances reaping at once is safe:
// exactly one wins each row.
//
// attempts is NOT incremented here, and not decremented either. It was incremented
// when the task was claimed, which is precisely what makes a task that kills its
// process consume an attempt — without that, a handler that reliably OOMs the box
// would retry forever, taking a worker down each time. The only path that refunds an
// attempt is releaseTask, and it can only be reached by this process cancelling its
// own handler during shutdown, where the interruption is known to be deliberate.
func (s *Service) reapOnce(ctx context.Context) error {
	now := s.now()
	expired, err := expiredLeases(ctx, s.db, now.UnixNano(), reapBatch)
	if err != nil {
		return err
	}

	for _, e := range expired {
		const reason = "queue: lease expired (the worker may have crashed)"

		var held bool
		if e.Attempts >= e.MaxAttempts {
			held, err = finishTask(ctx, s.db, e.ID, e.LeaseToken, StatusDead, reason, now.UnixNano())
		} else {
			delay := backoff(e.Attempts, s.RetryBase(), s.RetryMax(), s.jitter)
			held, err = retryTask(ctx, s.db, e.ID, e.LeaseToken,
				now.Add(delay).UnixNano(), reason, now.UnixNano())
		}
		if err != nil {
			return err
		}
		if !held {
			continue // another instance reaped it, or the worker came back in time
		}

		// The attempts row is what makes a crash visible in the failure log. Written
		// only after the status change was accepted, for the same reason as in
		// runOne: a log entry for a change that did not happen is worse than none.
		//
		// finished_at is the lease expiry rather than now: that is when the task
		// stopped being worked on, as far as anything can tell.
		if err := insertAttempt(ctx, s.db, attemptRow{
			TaskID:     e.ID,
			Attempt:    e.Attempts,
			Outcome:    OutcomeLost,
			Worker:     e.Worker,
			Error:      reason,
			StartedAt:  e.StartedAt,
			FinishedAt: e.LeaseUntil,
		}); err != nil {
			s.log.Error("queue: could not record a lost attempt", "task", e.ID, "err", err)
		}
		s.log.Warn("queue: reclaimed a task from a lost worker",
			"task", e.ID, "worker", e.Worker, "attempt", e.Attempts)
	}
	if len(expired) > 0 {
		s.nudge()
	}
	return nil
}

// sweepOrphans reports pending tasks whose kind no process here can run, and
// eventually declares them dead.
//
// Such a task is never claimed (claimTasks filters on the registry), which is what
// makes a rolling deployment safe but would also let a mistyped kind pile up in
// silence. Three things prevent that, and this is the second and third of them: a
// warning every sweep, and death after OrphanGrace.
//
// The grace period has to be much longer than a deployment takes, so instances never
// kill each other's work while one of them is still the old build. It is deliberately
// not configurable to "never": a task waiting forever with no handler is invisible
// rot, and offering a switch for it would be offering a way to hide it.
func (s *Service) sweepOrphans(ctx context.Context) error {
	kinds := s.reg.Kinds()

	counts, err := orphanCounts(ctx, s.db, kinds)
	if err != nil {
		return err
	}
	if len(counts) > 0 {
		s.log.Warn("queue: pending tasks have no handler in this process",
			"kinds", counts, "grace", s.OrphanGrace())
	}

	// Killing requires knowing the whole application's registry. A process holding a
	// subset would declare other people's work dead.
	if !s.reg.complete {
		return nil
	}

	cutoff := s.now().Add(-s.OrphanGrace()).UnixNano()
	orphans, err := orphanTasks(ctx, s.db, kinds, cutoff, orphanBatch)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		now := s.now().UnixNano()
		reason := fmt.Sprintf("queue: no handler registered for kind %q", o.Kind)

		changed, err := killPending(ctx, s.db, o.ID, reason, now)
		if err != nil {
			return err
		}
		if !changed {
			continue // claimed or cancelled between the scan and here
		}
		// One attempts row per task rather than one bulk UPDATE, so the failure log
		// explains why each one stopped. Attempt 0 because it never ran: no attempt
		// number was ever consumed, and pretending otherwise would put a gap in the
		// numbering of a task somebody later retries.
		if err := insertAttempt(ctx, s.db, attemptRow{
			TaskID:     o.ID,
			Attempt:    0,
			Outcome:    OutcomeFailed,
			Worker:     s.workerID,
			Error:      reason,
			StartedAt:  now,
			FinishedAt: now,
		}); err != nil {
			s.log.Error("queue: could not record an orphan's death", "task", o.ID, "err", err)
		}
		s.log.Warn("queue: declared an orphan task dead", "task", o.ID, "kind", o.Kind)
	}
	return nil
}

// pruneOnce enforces the retention windows.
//
// This is where retention belongs, and it is worth saying why it is not on the write
// path the way admin_login_attempts does it (see throttle.go). That table's size is
// proportional to recent activity and there was no scheduler at the time; neither
// holds here. The rows to delete have nothing to do with the task being written, and
// a DELETE on the claim path would add latency to every single task.
//
// Batched, with a cap on batches per tick, so one tick's work is bounded. Ids are
// read into Go first because MySQL refuses a LIMIT inside an IN (SELECT ...)
// subquery, and an unbounded DELETE on this table is how a prune turns into a
// lock-up. Two round trips beat a dialect branch — the same trade group_crud.go
// makes when it counts permissions in Go instead of with json_array_length.
//
// pending and running are never pruned, however old. An orphan is killed by
// sweepOrphans, which is a status change; deleting live work would be data loss.
func (s *Service) pruneOnce(ctx context.Context) error {
	// One entry per terminal status. A cancelled task keeps the succeeded window
	// rather than the dead one: both are outcomes nobody needs to come back to, and
	// saying so in one line beats implying it with two map entries. No zero check —
	// both accessors go through Service.dur, which substitutes a positive default.
	for _, w := range []struct {
		status string
		window time.Duration
	}{
		{StatusSucceeded, s.RetentionSucceeded()},
		{StatusCancelled, s.RetentionSucceeded()},
		{StatusDead, s.RetentionDead()},
	} {
		status := w.status
		cutoff := s.now().Add(-w.window).UnixNano()
		for range pruneMaxBatches {
			ids, err := prunableIDs(ctx, s.db, status, cutoff, pruneBatch)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			if err := deleteTasks(ctx, s.db, ids); err != nil {
				return err
			}
			if len(ids) < pruneBatch {
				break
			}
		}
	}

	// Attempts and tasks are deleted as two statements by design, so an interruption
	// between them leaves log rows with no task. This collects them. The
	// NOT IN (SELECT ...) shape is expensive, which is why it runs at the prune
	// interval and with a small cap rather than on any hot path.
	if _, err := deleteOrphanAttempts(ctx, s.db, orphanAttemptCap); err != nil {
		return err
	}
	return nil
}
