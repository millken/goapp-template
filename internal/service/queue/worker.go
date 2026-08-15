package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// The worker: one poller, a semaphore of executor slots, and one heartbeat.
//
// One poller rather than N independently-polling goroutines, because a claim round
// is one query that hands out a batch. With N pollers the same work costs N queries,
// and on SQLite every one of them takes the database's single write lock. Batching
// also amortises the round trip, which matters most exactly when there is a backlog.

// errShuttingDown marks a task interrupted because this process is stopping.
//
// A sentinel, not a status, because the difference it expresses is about knowledge
// rather than about the task: we know this interruption was deliberate and that a
// replacement process is starting, so the attempt is refunded. Every other
// interruption — a crash, a stall, a lost lease — is indistinguishable from a
// failure and is charged as one.
var errShuttingDown = errors.New("queue: process is shutting down")

// pollLoop claims and dispatches until claimCtx is cancelled, then waits for the
// tasks it has in flight.
//
// It takes two contexts. claimCtx ends the claiming; taskCtx is the parent of every
// handler's context and is cancelled only when Stop has run out of patience. That
// split is the drain: claiming stops at once, running work keeps its context.
func (s *Service) pollLoop(claimCtx, taskCtx context.Context) {
	// slots is the concurrency limit and the in-flight count in one object: a send
	// takes a slot, a receive returns one, and len() is how many are busy. A
	// buffered channel rather than x/sync/semaphore, because nothing here needs
	// weighted acquisition — and rather than a hand-kept integer, because that
	// integer has to be right in three places and this cannot drift.
	slots := make(chan struct{}, s.Concurrency())

	// Waiting for the executors here is where drain actually happens: pollLoop
	// returning is what lets Start's WaitGroup complete, which is what Stop waits on.
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		if claimCtx.Err() != nil {
			return
		}

		free := cap(slots) - len(slots)
		claimed := 0
		if free > 0 {
			// Ask for more candidates than there are free slots: a claim lost to
			// another worker costs a candidate, and coming back for it would cost
			// another query. The +4 keeps that true at concurrency 1.
			batch, err := s.claimBatch(claimCtx, free*2+4)
			if err != nil {
				s.log.Error("queue: claim failed", "err", err)
			}
			// Everything claimed is run, even beyond `free`: the row already says
			// running, so leaving one undispatched would strand it until its lease
			// expired.
			for _, t := range batch {
				slots <- struct{}{}
				wg.Add(1)
				go func(t claimedTask) {
					defer wg.Done()
					defer func() {
						<-slots
						// Wake the poller: a free slot is new capacity, and waiting out
						// a poll interval to notice would idle it for no reason.
						s.nudge()
					}()
					s.runOne(taskCtx, t)
				}(t)
			}
			claimed = len(batch)
		}

		// A full batch means there is probably more waiting: go again immediately.
		if claimed > 0 && claimed >= free {
			continue
		}

		// Idle, or every slot busy. Wake on the timer, on a local enqueue or a slot
		// coming free (both nudge), or on shutdown.
		timer := time.NewTimer(s.PollInterval())
		select {
		case <-claimCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.wake:
			timer.Stop()
		}
	}
}

// claimBatch takes up to limit due tasks of the kinds this process handles.
func (s *Service) claimBatch(ctx context.Context, limit int) ([]claimedTask, error) {
	now := s.now()
	return claimTasks(ctx, s.db, s.reg.Kinds(), limit,
		now.UnixNano(), now.Add(s.LeaseTTL()).UnixNano(), s.workerID)
}

// runOne executes one claimed task and records the outcome.
//
// The order of the two writes is fixed and load-bearing: the authoritative status
// change first, and the attempts row only if that change was accepted. Reversed, a
// worker whose lease had been reclaimed would leave a convincing log entry for a
// result that was discarded. This way the worst case is a missing log line, never a
// duplicated execution — which is also why the pair is not a transaction.
func (s *Service) runOne(parent context.Context, t claimedTask) {
	timeout := s.TaskTimeout()
	if t.TimeoutMS > 0 {
		timeout = time.Duration(t.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	hold := leaseHold{ID: t.ID, Token: t.LeaseToken}
	s.trackInFlight(hold, cancel)
	defer s.untrackInFlight(hold)

	task := &Task{
		ID:          t.ID,
		Kind:        t.Kind,
		Payload:     t.Payload,
		Attempt:     t.Attempt,
		MaxAttempts: t.MaxAttempts,
		ScheduleID:  t.ScheduleID,
	}

	entry, ok := s.reg.lookup(t.Kind)
	if !ok {
		// claimTasks filters on the registry, so this is unreachable unless the
		// registry changed underneath — which the API does not allow. Handled rather
		// than ignored because "unreachable" and "cannot happen" are different.
		s.finish(t, task, OutcomeFailed,
			fmt.Errorf("queue: no handler for kind %q in this process", t.Kind))
		return
	}

	err := s.invoke(ctx, entry.fn, task)

	// Distinguish our own shutdown from anything else. parent is cancelled only by
	// Stop's second phase, so this test is what earns the refund.
	//
	// The handler's own error is kept alongside the sentinel rather than replaced by
	// it. A handler that fails for its own reasons while wrapping a cancellation —
	// "charge customer: context canceled" — is the case that matters: replacing the
	// message loses the only account of what the interrupted attempt was doing, on the
	// one path that also refunds the attempt, so nothing else records it either.
	if err != nil && parent.Err() != nil && errors.Is(err, context.Canceled) {
		err = fmt.Errorf("%w: %w", errShuttingDown, err)
	}
	s.finish(t, task, outcomeFor(ctx, err), err)
}

// invoke calls a handler with a panic guard.
//
// A business handler's panic must not take down the process it happens to be running
// in — which, for the embedded worker, is the web server. The stack is kept and
// stored as the attempt's error, because a panic with no stack is the one failure
// nobody can act on from an admin screen.
func (s *Service) invoke(ctx context.Context, fn HandlerFunc, t *Task) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("queue: handler panicked: %v\n\n%s", rec, debug.Stack())
		}
	}()
	return fn(ctx, t)
}

// finish applies the outcome: succeed, retry with backoff, give up, or hand back.
func (s *Service) finish(t claimedTask, task *Task, outcome string, cause error) {
	// A fresh context, deliberately: the task's own context may already be
	// cancelled or timed out, and the result still has to be written. Bounded so a
	// wedged database cannot hold a worker slot open indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := s.now().UnixNano()
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}

	var held bool
	var err error
	switch {
	case cause == nil:
		held, err = finishTask(ctx, s.db, t.ID, t.LeaseToken, StatusSucceeded, "", now)

	case errors.Is(cause, errShuttingDown):
		// Refunded, and eligible immediately: the replacement process should pick it
		// up as soon as it starts polling.
		held, err = releaseTask(ctx, s.db, t.ID, t.LeaseToken, now, msg, now)

	case errors.Is(cause, ErrPermanent):
		// The input is wrong, so retrying cannot help. Straight to dead, skipping the
		// remaining attempts and the backoff they would have waited out.
		held, err = finishTask(ctx, s.db, t.ID, t.LeaseToken, StatusDead, msg, now)

	case task.Attempt >= task.MaxAttempts:
		held, err = finishTask(ctx, s.db, t.ID, t.LeaseToken, StatusDead, msg, now)

	default:
		delay := backoff(task.Attempt, s.RetryBase(), s.RetryMax(), s.jitter)
		held, err = retryTask(ctx, s.db, t.ID, t.LeaseToken,
			s.now().Add(delay).UnixNano(), msg, now)
	}

	if err != nil {
		s.log.Error("queue: could not record a task outcome",
			"task", t.ID, "kind", t.Kind, "err", err)
		return
	}
	if !held {
		// Fencing refused the write. Somebody else owns this task now, so this
		// result is not ours to report — and no attempts row is written either, or
		// the log would describe an execution whose result was thrown away.
		s.log.Warn("queue: discarding a result for a task we no longer hold",
			"task", t.ID, "kind", t.Kind, "attempt", task.Attempt, "outcome", outcome)
		return
	}

	if err := insertAttempt(ctx, s.db, attemptRow{
		TaskID:     t.ID,
		Attempt:    task.Attempt,
		Outcome:    outcome,
		Worker:     s.workerID,
		Error:      msg,
		StartedAt:  t.StartedAt,
		FinishedAt: now,
	}); err != nil {
		s.log.Error("queue: could not record an attempt", "task", t.ID, "err", err)
	}
}

// outcomeFor names what happened, for the failure log.
//
// A timeout is distinguished from a plain failure because they call for different
// responses — a slow dependency versus a broken one — and the admin screen cannot
// tell them apart from the error text alone.
func outcomeFor(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return OutcomeSucceeded
	case errors.Is(err, errShuttingDown):
		return OutcomeCancelled
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return OutcomeTimeout
	default:
		return OutcomeFailed
	}
}

// heartbeatLoop renews this process's leases and cancels the tasks it has lost.
func (s *Service) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(s.Heartbeat())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.heartbeatOnce(ctx); err != nil {
				// Failing to renew is not fatal: the leases outlive several ticks, so
				// a database hiccup costs nothing as long as it ends before the TTL.
				s.log.Warn("queue: heartbeat failed", "err", err)
			}
		}
	}
}

// heartbeatOnce extends every lease this process holds, in one statement, and
// cancels the handlers of any task it no longer holds.
//
// That second half is the whole implementation of "cancel a running task" from the
// admin area: cancelling sets the status, which makes the renewal skip the row,
// which makes it appear in the lost list here. No extra column, no polling by the
// handler — and the latency is therefore one heartbeat interval, which is what the
// admin screen's wording promises.
func (s *Service) heartbeatOnce(ctx context.Context) error {
	holds := s.inFlightHolds()
	if len(holds) == 0 {
		return nil
	}

	now := s.now()
	lost, err := renewLeases(ctx, s.db, holds, s.workerID,
		now.Add(s.LeaseTTL()).UnixNano(), now.UnixNano())
	if err != nil {
		return err
	}
	for _, h := range lost {
		s.mu.Lock()
		cancel := s.inFlight[h]
		s.mu.Unlock()
		if cancel == nil {
			continue // finished between the snapshot and now
		}
		s.log.Warn("queue: lost a task's lease, cancelling its handler",
			"task", h.ID, "token", h.Token)
		cancel()
	}
	return nil
}

// --- in-flight bookkeeping ---

func (s *Service) trackInFlight(h leaseHold, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight[h] = cancel
}

func (s *Service) untrackInFlight(h leaseHold) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, h)
}

func (s *Service) inFlightHolds() []leaseHold {
	s.mu.Lock()
	defer s.mu.Unlock()
	holds := make([]leaseHold, 0, len(s.inFlight))
	for h := range s.inFlight {
		holds = append(holds, h)
	}
	return holds
}
