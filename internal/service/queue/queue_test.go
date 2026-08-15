package queue

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// startFor builds a service around cfg and Starts it, cleaning up on the way out.
// It uses the real clock: these are the tests about goroutines and shutdown, and the
// only durations they wait on are their own.
func startFor(t *testing.T, db *sqldb.DB, reg *Registry, cfg *Config) *Service {
	t.Helper()
	s := New(cfg, db, reg, discardLogger())
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

// --- config validation ---

func TestStart_RequiresConfigAndDB(t *testing.T) {
	db := openMigrated(t, memDSN("start-guards"))

	t.Run("missing section", func(t *testing.T) {
		s := New(nil, db, NewRegistry(), discardLogger())
		err := s.Start(context.Background())
		if err == nil || !strings.Contains(err.Error(), "config section missing") {
			t.Errorf("err = %v, want the missing-section error", err)
		}
	})

	t.Run("missing db", func(t *testing.T) {
		s := New(&Config{Concurrency: ptr(1)}, nil, NewRegistry(), discardLogger())
		err := s.Start(context.Background())
		if err == nil || !strings.Contains(err.Error(), "db component") {
			t.Errorf("err = %v, want the missing-db error", err)
		}
	})

	// Absent concurrency is refused rather than read as zero. Zero is a real setting —
	// "admin screens only" — and a process that was meant to run work but quietly runs
	// none is the worst outcome available here.
	t.Run("absent concurrency", func(t *testing.T) {
		s := New(&Config{}, db, NewRegistry(), discardLogger())
		err := s.Start(context.Background())
		if err == nil || !strings.Contains(err.Error(), "concurrency must be set") {
			t.Errorf("err = %v, want the unset-concurrency error", err)
		}
	})
}

// The timing invariants are refused, not corrected. A corrected value would hide the
// fact that the operator asked for something impossible, and each of these produces a
// specific and confusing runtime failure.
func TestStart_RejectsImpossibleTimings(t *testing.T) {
	db := openMigrated(t, memDSN("start-timings"))

	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			// One missed renewal would then lose the task.
			name: "lease no longer than two heartbeats",
			cfg:  Config{Concurrency: ptr(1), LeaseTTL: 30 * time.Second, Heartbeat: 20 * time.Second},
			want: "twice heartbeat",
		},
		{
			name: "retry_max below retry_base",
			cfg:  Config{Concurrency: ptr(1), RetryBase: time.Hour, RetryMax: time.Minute},
			want: "retry_max",
		},
		{
			name: "negative concurrency",
			cfg:  Config{Concurrency: ptr(-1)},
			want: "negative",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(&c.cfg, db, NewRegistry(), discardLogger())
			err := s.Start(context.Background())
			if err == nil {
				t.Fatalf("Start accepted %+v", c.cfg)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

// The defaults must satisfy their own invariants. Easy to break by editing one
// constant, and the failure would only show up in a project that configured nothing.
func TestStart_DefaultsAreSelfConsistent(t *testing.T) {
	db := openMigrated(t, memDSN("start-defaults"))
	s := New(&Config{Concurrency: ptr(0)}, db, NewRegistry(), discardLogger())
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("the built-in defaults do not satisfy validateConfig: %v", err)
	}
}

// A bad timezone stops startup. Falling back to UTC would turn "0 3 * * *" into 11am
// for a UTC+8 business, and that is found out from a missing report rather than a log
// line nobody reads.
func TestStart_RejectsABadTimezone(t *testing.T) {
	db := openMigrated(t, memDSN("start-tz"))
	s := New(&Config{Concurrency: ptr(0), Timezone: "Mars/Olympus_Mons"}, db,
		NewRegistry(), discardLogger())
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timezone") {
		t.Errorf("err = %v, want a timezone error", err)
	}
}

// tzdata is compiled in, so a real zone resolves even in a scratch container.
func TestStart_ResolvesARealTimezone(t *testing.T) {
	db := openMigrated(t, memDSN("start-tz-ok"))
	s := New(&Config{Concurrency: ptr(0), Timezone: "Asia/Shanghai"}, db,
		NewRegistry(), discardLogger())
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := s.Location().String(); got != "Asia/Shanghai" {
		t.Errorf("Location = %q", got)
	}
}

// A cron plan whose kind has no handler would fire forever into a queue nobody drains.
// Refused at startup, where the failure is legible.
func TestStart_RejectsACronWithNoHandler(t *testing.T) {
	db := openMigrated(t, memDSN("start-cron-nohandler"))
	reg := NewRegistry()
	reg.Cron("nightly", "@daily", "missing", nil)

	s := New(&Config{Concurrency: ptr(0)}, db, reg, discardLogger())
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("err = %v, want the no-handler error", err)
	}
}

// --- lifecycle ---

// concurrency=0 means "admin screens only": the schema is migrated and the plans are
// synced, but nothing runs. Stop then has nothing to wait for.
func TestStartStop_ConcurrencyZeroRunsNothing(t *testing.T) {
	db := openMigrated(t, memDSN("life-zero"))
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error {
		t.Error("a handler ran with concurrency=0")
		return nil
	})
	reg.Cron("nightly", "@daily", "k", nil)

	s := New(&Config{Concurrency: ptr(0)}, db, reg, discardLogger())
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Plans were still synced, because the admin screens need them.
	if n := countRows(t, db, `SELECT COUNT(*) FROM queue_schedules`); n != 1 {
		t.Errorf("%d plans synced, want 1", n)
	}
	// A due task is left alone.
	seedTask(t, db, seed{Kind: "k"})
	time.Sleep(50 * time.Millisecond)
	if got := readTask(t, db, 1).Status; got != StatusPending {
		t.Errorf("status = %q, want pending", got)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop with concurrency=0: %v", err)
	}
}

func TestStop_BeforeStartIsSafe(t *testing.T) {
	db := openMigrated(t, memDSN("life-nostart"))
	s := New(&Config{Concurrency: ptr(1)}, db, NewRegistry(), discardLogger())
	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop without Start: %v", err)
	}
}

// The happy path: a task enqueued after Start runs without waiting out a poll
// interval, because Enqueue nudges the poller.
func TestStartStop_RunsAnEnqueuedTask(t *testing.T) {
	db := openMigrated(t, memDSN("life-run"))
	var ran atomic.Int32
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error {
		ran.Add(1)
		return nil
	})

	// A poll interval far longer than the test: if the task runs, it is because the
	// nudge woke the poller, not because the timer fired.
	s := startFor(t, db, reg, &Config{Concurrency: ptr(2), PollInterval: time.Hour})

	if _, err := s.Enqueue(context.Background(), "k", nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, time.Second, func() bool { return ran.Load() == 1 })

	if err := s.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// THE trap this package is most likely to regress on.
//
// runServe passes cmd.Context(), which main.go wires to signal.NotifyContext. So the
// startup context is cancelled the moment Ctrl-C is pressed — which happens while the
// process is still serving, because eng.Serve() has its own signal handling and the
// deferred Stop does not run until it returns. If the run context were derived from the
// startup context, every worker would die at that instant and in-flight tasks would be
// killed outright rather than drained.
//
// context.WithoutCancel in Start is what prevents it, and nothing else in this suite
// would notice if it were removed: every other test would stay green, because they all
// keep their startup context alive.
//
// Note what is NOT asserted: Start itself may legitimately fail on an already-cancelled
// context. Migration and schedule sync deliberately use the startup context, because
// main.go's signal handling exists precisely so that a slow startup is interruptible.
// The claim is about the goroutines outliving that context, not about ignoring it.
func TestStart_WorkersOutliveTheStartupContext(t *testing.T) {
	db := openMigrated(t, memDSN("life-withoutcancel"))
	var ran atomic.Int32
	reg := NewRegistry()
	reg.Handle("k", func(context.Context, *Task) error {
		ran.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	s := New(&Config{Concurrency: ptr(1), PollInterval: 10 * time.Millisecond},
		db, reg, discardLogger())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	// SIGINT arrives. The process keeps serving; Stop has not run.
	cancel()

	// Work enqueued after the signal still runs, which is what "drain" means for the
	// requests eng.Serve() is still finishing.
	seedTask(t, db, seed{Kind: "k"})
	waitFor(t, 2*time.Second, func() bool { return ran.Load() == 1 })
}

// Drain: Stop stops claiming at once, but a task already running keeps its context and
// is allowed to finish.
func TestStop_LetsAnInFlightTaskFinish(t *testing.T) {
	db := openMigrated(t, memDSN("life-drain"))
	release := make(chan struct{})
	var finished atomic.Bool

	reg := NewRegistry()
	reg.Handle("k", func(ctx context.Context, _ *Task) error {
		<-release
		finished.Store(true)
		return nil
	})
	s := startFor(t, db, reg, &Config{
		Concurrency: ptr(1), PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 5 * time.Second,
	})

	id := seedTask(t, db, seed{Kind: "k"})
	waitFor(t, time.Second, func() bool {
		return readTask(t, db, id).Status == StatusRunning
	})

	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop(context.Background()) }()

	// Stop must not have returned yet: the task is still running.
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned while a task was still running: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the task finished")
	}
	if !finished.Load() {
		t.Error("the in-flight task was killed rather than drained")
	}
	if got := readTask(t, db, id).Status; got != StatusSucceeded {
		t.Errorf("status = %q, want succeeded", got)
	}
}

// A handler that ignores its context must not hang shutdown forever. The grace
// period is what bounds the wait, with no deadline on the context at all.
func TestStop_GivesUpOnAStubbornHandler(t *testing.T) {
	db := openMigrated(t, memDSN("life-stubborn"))
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	reg := NewRegistry()
	reg.Handle("k", func(ctx context.Context, _ *Task) error {
		<-blocked // never returns during the test, and ignores ctx entirely
		return nil
	})
	s := New(&Config{
		Concurrency: ptr(1), PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 100 * time.Millisecond,
	}, db, reg, discardLogger())
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	id := seedTask(t, db, seed{Kind: "k"})
	waitFor(t, time.Second, func() bool {
		return readTask(t, db, id).Status == StatusRunning
	})

	start := time.Now()
	err := s.Stop(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Stop reported success though a handler never returned")
	}
	if !strings.Contains(err.Error(), "grace period") {
		t.Errorf("err = %v, want it to name the grace period", err)
	}
	// grace (100ms) + the 2s it waits after cancelling, with room to spare.
	if elapsed > 5*time.Second {
		t.Errorf("Stop took %s; it must bound its own wait", elapsed)
	}
}

// The drain is sized by ShutdownGrace, not by whatever deadline the caller
// happened to set.
//
// Both production call sites pass queueStopTimeout — two minutes — as a backstop
// against a handler that ignores cancellation. Stop used to take that deadline
// INSTEAD of the grace whenever one was present, which made queue.shutdown_grace
// dead config in the only paths that use it: Ctrl-C sat for two minutes and then
// logged an expiry quoting the 30s grace it had never waited.
//
// The reverse direction is asserted too: a deadline tighter than the grace is a
// real ceiling, since a caller that has only 100ms left cannot be made to wait 5s.
func TestStop_DrainsForTheGraceNotTheCallersDeadline(t *testing.T) {
	cases := []struct {
		name     string
		grace    time.Duration
		deadline time.Duration
	}{
		{"deadline far beyond the grace", 100 * time.Millisecond, time.Minute},
		{"deadline tighter than the grace", 5 * time.Second, 100 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := openMigrated(t, memDSN("life-grace-"+strings.ReplaceAll(tc.name, " ", "-")))
			blocked := make(chan struct{})
			t.Cleanup(func() { close(blocked) })

			reg := NewRegistry()
			reg.Handle("k", func(ctx context.Context, _ *Task) error {
				<-blocked // ignores ctx, so only the timers end this
				return nil
			})
			s := New(&Config{
				Concurrency: ptr(1), PollInterval: 10 * time.Millisecond,
				ShutdownGrace: tc.grace,
			}, db, reg, discardLogger())
			if err := s.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}

			id := seedTask(t, db, seed{Kind: "k"})
			waitFor(t, time.Second, func() bool {
				return readTask(t, db, id).Status == StatusRunning
			})

			ctx, cancel := context.WithTimeout(context.Background(), tc.deadline)
			defer cancel()
			start := time.Now()
			err := s.Stop(ctx)
			elapsed := time.Since(start)

			if err == nil {
				t.Error("Stop reported success though a handler never returned")
			}
			// Whichever of the two is smaller, plus the fixed 2s after cancelling, plus
			// slack for a loaded machine. The slack is small enough that waiting for the
			// LARGER of the two still fails: a minute in the first case, 5s in the second.
			want := min(tc.grace, tc.deadline) + 2*time.Second
			if elapsed > want+3*time.Second {
				t.Errorf("Stop took %s; grace %s and deadline %s should have bounded it at ~%s",
					elapsed, tc.grace, tc.deadline, want)
			}
		})
	}
}

// --- Enqueue ---

func TestEnqueue_Options(t *testing.T) {
	db := openMigrated(t, memDSN("enqueue-opts"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("k", noop, WithMaxAttempts(7), WithPriority(3), WithTimeout(90*time.Second))
	s, clock := newTestService(t, db, reg, nil)

	// Registration supplies the defaults, so callers do not repeat them.
	id, err := s.Enqueue(ctx, "k", map[string]int{"n": 1})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	var (
		payload          string
		priority, maxAtt int
		timeoutMS        int
		runAt            int64
	)
	if err := db.QueryRowContext(ctx,
		`SELECT payload, priority, max_attempts, timeout_ms, run_at FROM queue_tasks WHERE id = ?`,
		id).Scan(&payload, &priority, &maxAtt, &timeoutMS, &runAt); err != nil {
		t.Fatal(err)
	}
	if payload != `{"n":1}` || priority != 3 || maxAtt != 7 || timeoutMS != 90_000 {
		t.Errorf("row = %s/%d/%d/%d", payload, priority, maxAtt, timeoutMS)
	}
	if runAt != clock.now().UnixNano() {
		t.Errorf("run_at = %d, want now for a plain enqueue", runAt)
	}

	// Per-call options win over the registration.
	id2, err := s.Enqueue(ctx, "k", nil, MaxAttempts(1), Priority(9), After(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue with options: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT priority, max_attempts, run_at FROM queue_tasks WHERE id = ?`, id2).
		Scan(&priority, &maxAtt, &runAt); err != nil {
		t.Fatal(err)
	}
	if priority != 9 || maxAtt != 1 {
		t.Errorf("options did not override the registration: priority=%d maxAttempts=%d", priority, maxAtt)
	}
	if want := clock.now().Add(time.Hour).UnixNano(); runAt != want {
		t.Errorf("run_at = %d, want %d (the delay)", runAt, want)
	}
}

func TestEnqueue_AtAndUnique(t *testing.T) {
	db := openMigrated(t, memDSN("enqueue-at"))
	ctx := context.Background()
	reg := NewRegistry()
	reg.Handle("k", noop)
	s, _ := newTestService(t, db, reg, nil)

	when := time.Date(2030, 1, 2, 3, 4, 0, 0, time.UTC)
	id, err := s.Enqueue(ctx, "k", nil, At(when))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := readTask(t, db, id).RunAt; got != when.UnixNano() {
		t.Errorf("run_at = %d, want %d", got, when.UnixNano())
	}

	if _, err := s.Enqueue(ctx, "k", nil, Unique("only-once")); err != nil {
		t.Fatalf("first unique enqueue: %v", err)
	}
	if _, err := s.Enqueue(ctx, "k", nil, Unique("only-once")); err == nil {
		t.Error("a duplicate unique key was accepted")
	}
}

// An unregistered kind is allowed. That is what makes deploying a producer ahead of
// its consumer safe: the task waits, visibly, until a process that handles it exists.
func TestEnqueue_AllowsAnUnregisteredKind(t *testing.T) {
	db := openMigrated(t, memDSN("enqueue-unknown"))
	s, _ := newTestService(t, db, NewRegistry(), nil)

	id, err := s.Enqueue(context.Background(), "not:registered:yet", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	row := readTask(t, db, id)
	if row.Status != StatusPending {
		t.Errorf("status = %q, want pending", row.Status)
	}
	if row.MaxAttempts != defaultMaxAttempts {
		t.Errorf("max_attempts = %d, want the configured default %d", row.MaxAttempts, defaultMaxAttempts)
	}
}

func TestEnqueue_RejectsAnUnmarshalablePayload(t *testing.T) {
	db := openMigrated(t, memDSN("enqueue-badpayload"))
	s, _ := newTestService(t, db, NewRegistry(), nil)
	if _, err := s.Enqueue(context.Background(), "k", func() {}); err == nil {
		t.Error("Enqueue accepted a func as a payload")
	}
}

// --- accessors ---

func TestAccessors_NilConfigIsSafe(t *testing.T) {
	// Tests may construct a service without Start, so every accessor has to tolerate a
	// nil config — the same contract the storage service's accessors have.
	s := New(nil, nil, nil, discardLogger())
	if got := s.Concurrency(); got != 0 {
		t.Errorf("Concurrency = %d, want 0", got)
	}
	if got := s.LeaseTTL(); got != defaultLeaseTTL {
		t.Errorf("LeaseTTL = %s, want the default", got)
	}
	if got := s.MaxAttempts(); got != defaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want the default", got)
	}
	if got := s.AdminPageSize(); got != defaultAdminPageSize {
		t.Errorf("AdminPageSize = %d, want the default", got)
	}
	if s.Location() == nil {
		t.Error("Location must never be nil")
	}
}

// A negative duration is treated as unset rather than honoured: every duration here
// means "how long", and none has a meaning below zero.
func TestAccessors_NegativeDurationsFallBackToDefaults(t *testing.T) {
	s := New(&Config{LeaseTTL: -time.Second, PollInterval: -1}, nil, nil, discardLogger())
	if got := s.LeaseTTL(); got != defaultLeaseTTL {
		t.Errorf("LeaseTTL = %s, want the default", got)
	}
	if got := s.PollInterval(); got != defaultPollInterval {
		t.Errorf("PollInterval = %s, want the default", got)
	}
}

func TestParseCronIsTheOnlyParser(t *testing.T) {
	// Exposed as a method so the admin form validates and previews with exactly what
	// the scheduler runs on. A second implementation is how "saved fine, never fires"
	// happens.
	s := New(nil, nil, nil, discardLogger())
	if _, err := s.ParseCron("0 3 * * *"); err != nil {
		t.Errorf("ParseCron: %v", err)
	}
	if _, err := s.ParseCron("nonsense"); err == nil {
		t.Error("ParseCron accepted nonsense")
	}
	if _, err := s.ParseCron("30 2 30 2 *"); err != nil {
		// Parses fine; only Next reports it as unsatisfiable.
		t.Errorf("ParseCron on an unsatisfiable expression: %v", err)
	}
	sched, _ := s.ParseCron("30 2 30 2 *")
	if _, err := sched.Next(time.Now()); !errors.Is(err, ErrCronUnsatisfiable) {
		t.Errorf("Next = %v, want ErrCronUnsatisfiable", err)
	}
}

// waitFor polls a condition. Used only by the lifecycle tests, which are about real
// goroutines; everything else in this package injects a clock and never waits.
func waitFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", limit)
}
