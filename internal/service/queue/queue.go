// Package queue is the task queue: a pure database queue for one-shot, delayed
// and periodic (cron) work, with retries, a per-attempt failure log, and an admin
// surface for inspecting and intervening.
//
// It is one of the template's optional components, and it depends on db. The whole
// queue is three tables (see migrations/), so pointing the application at MySQL or
// PostgreSQL instead of SQLite is still just a change of DSN. Nothing here needs
// Redis.
//
// # The one idea the design rests on
//
// Periodicity is a property of the PRODUCER, not of the task. queue_schedules
// produces; queue_tasks only ever knows a run_at. One-shot is run_at = now,
// delayed is run_at = now + d, and periodic is a plain task row the scheduler
// inserts each time a plan comes due. So retries, backoff, timeouts, the failure
// log and every admin action are one implementation serving all three, and a cron
// plan's history is just `WHERE schedule_id = ?`.
//
// # Concurrency
//
// Claiming is a two-phase optimistic compare-and-swap (claim.go has the argument
// for why it cannot double-execute or lose a task), with no FOR UPDATE SKIP LOCKED
// so that one code path serves all three dialects. A claimed task is held by a
// lease with a random fencing token; a worker that stalls past its lease has its
// task reclaimed and cannot write a result afterwards.
//
// # Who owns what
//
// This package owns every statement against its own tables, including the ones the
// admin controllers run. That is a deliberate departure from internal/controller/
// admin/user_crud.go, which writes its own SQL: the admins table belongs to the
// admin area, whereas these tables belong here, and every write to them has to
// respect the same fencing and status invariants the worker does. A "retry" query
// copied into a controller that forgets one predicate is how a running task gets
// quietly resurrected.
//
// # Imports
//
// This package must not import internal/app or internal/config. internal/config
// imports it (for Config), so either edge would close a cycle. Handlers reach
// application services by closing over them in internal/tasks, which is why that
// package exists.
package queue

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/dnsoa/go/sqldb"

	// tzdata so a queue.timezone of "Asia/Shanghai" resolves in a scratch or
	// distroless container, which ships no zoneinfo. It costs about 450KB in the
	// binary — paid only by builds that keep this component, since goappctl strips
	// the whole directory otherwise. Without it, LoadLocation fails and Start
	// refuses to run, which is correct but unhelpful when the fix is a build tag
	// nobody remembers.
	_ "time/tzdata"
)

// Defaults applied by the accessors when a field is left empty.
const (
	defaultPollInterval  = time.Second
	defaultTickInterval  = 5 * time.Second
	defaultLeaseTTL      = time.Minute
	defaultHeartbeat     = 20 * time.Second
	defaultTaskTimeout   = 5 * time.Minute
	defaultShutdownGrace = 30 * time.Second
	defaultMaxAttempts   = 3
	defaultRetryBase     = 15 * time.Second
	defaultRetryMax      = time.Hour
	defaultOrphanGrace   = 24 * time.Hour
	defaultRetentionOK   = 7 * 24 * time.Hour
	defaultRetentionDead = 30 * 24 * time.Hour
	defaultPruneInterval = time.Hour
	defaultAdminPageSize = 25
)

// Batch sizes for the periodic sweeps. Fixed rather than configurable: they exist
// to bound the work one tick can do, and an operator turning them up is asking for
// the long lock this bound exists to prevent.
const (
	reapBatch        = 100
	orphanBatch      = 200
	pruneBatch       = 500
	pruneMaxBatches  = 20
	orphanAttemptCap = 500
)

// Config configures the queue service. A pointer in New lets Start distinguish
// "enabled but misconfigured" (nil) from "not enabled" (never Started).
type Config struct {
	// Concurrency is how many tasks this process runs at once. Zero disables the
	// worker subsystem entirely — no execution, no cron, no reaping, no pruning —
	// while leaving the service usable for the admin screens.
	//
	// A pointer because zero is a meaningful value and "absent" must not silently
	// mean it: a process that was meant to run work but quietly runs none is the
	// worst outcome available here, so an absent value is a startup error. Same
	// reasoning as storage.Config.Root having no default.
	Concurrency *int `yaml:"concurrency"`

	// PollInterval is how long an idle poller waits before looking again. A
	// process that enqueues a task wakes its own poller immediately, so this is
	// only the latency for work another process created.
	PollInterval time.Duration `yaml:"poll_interval"`
	// TickInterval drives cron firing, lease reaping and pruning. It is therefore
	// also the maximum lateness of a cron plan.
	TickInterval time.Duration `yaml:"tick_interval"`

	// LeaseTTL is how long a claim is good for without a heartbeat. Must exceed
	// twice Heartbeat, so one missed renewal does not lose the task.
	LeaseTTL time.Duration `yaml:"lease_ttl"`
	// Heartbeat is how often leases are renewed. It is also the upper bound on how
	// long "cancel this running task" takes to reach the handler, since that is
	// what a failed renewal signals.
	Heartbeat time.Duration `yaml:"heartbeat"`
	// TaskTimeout is the deadline on one attempt's context.
	//
	// It is deliberately NOT constrained against LeaseTTL, and the reason is the
	// heartbeat: a task's lease is renewed every Heartbeat for as long as it runs,
	// so a five-minute task under a one-minute lease is renewed four times and never
	// looks abandoned. Requiring TaskTimeout < LeaseTTL would tie the longest
	// permissible task to a number that only describes how long silence is tolerated,
	// and would force anyone with a slow job to widen their crash-detection window
	// for no reason.
	//
	// What DOES break the pairing is a process too wedged to run its own heartbeat —
	// CPU starvation, or a database it cannot reach. Then the lease lapses while the
	// handler is still going and the task is reclaimed. Fencing stops the stale
	// worker from writing a result, but its side effects have already happened, so
	// handlers that must not run twice should be idempotent. No configuration can fix
	// that; only the handler can.
	TaskTimeout time.Duration `yaml:"task_timeout"`
	// ShutdownGrace is how long Stop waits for in-flight tasks after it stops
	// claiming, before cancelling their contexts.
	ShutdownGrace time.Duration `yaml:"shutdown_grace"`

	// MaxAttempts is the default attempt ceiling, recorded onto each task when it
	// is enqueued — so changing it does not disturb work already queued.
	MaxAttempts int `yaml:"max_attempts"`
	// RetryBase and RetryMax bound the exponential backoff; see backoff.go.
	RetryBase time.Duration `yaml:"retry_base"`
	RetryMax  time.Duration `yaml:"retry_max"`

	// Timezone is an IANA name (for example Asia/Shanghai) that cron expressions
	// are interpreted in. Empty means the process's local zone. An unresolvable
	// name is a startup error, never a silent fall back to UTC: a server running
	// UTC while the business runs UTC+8 turns "0 3 * * *" into 11am, and finding
	// that out from a report is much more expensive than finding it out from a
	// failed start.
	Timezone string `yaml:"timezone"`

	// OrphanGrace is how long a pending task whose kind has no registered handler
	// is left alone before being declared dead. It has to be far longer than a
	// rolling deployment takes, so instances never kill each other's work; a day
	// is comfortably that.
	//
	// There is no "never" setting. A task that waits forever with no handler is
	// invisible rot, and offering that as a mode would be offering a way to hide
	// it. Set a longer duration if a day is not enough.
	OrphanGrace time.Duration `yaml:"orphan_grace"`

	// RetentionSucceeded and RetentionDead are how long terminal tasks (and their
	// attempts) are kept.
	//
	// Neither has a "keep forever" setting, for the same reason OrphanGrace has no
	// "never": unbounded growth is not a mode, it is a deferred outage. Set a long
	// duration if a long one is what you want.
	RetentionSucceeded time.Duration `yaml:"retention_succeeded"`
	RetentionDead      time.Duration `yaml:"retention_dead"`
	// PruneInterval is how often the retention sweep runs.
	PruneInterval time.Duration `yaml:"prune_interval"`

	// AdminPageSize is rows per page in the admin task list.
	AdminPageSize int `yaml:"admin_page_size"`
}

// Option configures the service at construction. Used for values that come from
// another component rather than from [queue] — the service container holds no
// *config.Config, so they arrive as constructor arguments.
type Option func(*Service)

// WithMigrations tells the service to apply its own schema, recording it under its
// own migration service name in the given table.
//
// The table comes from the db component (db.Service.MigrationTable). Omit the
// option — which is what a composition root does when [db.migrations] is absent —
// and the queue does not migrate either: an operator managing schema by hand gets
// the same answer from every component.
func WithMigrations(table string) Option {
	return func(s *Service) {
		s.migrateSchema = true
		s.migrationTable = table
	}
}

// Service is the queue. One instance per process, shared read-only by the
// controllers after Start.
type Service struct {
	cfg *Config
	db  *sqldb.DB
	reg *Registry
	log *slog.Logger

	migrateSchema  bool
	migrationTable string

	// loc is the zone cron expressions are evaluated in, resolved by Start.
	loc *time.Location
	// workerID identifies this process in queue_tasks.worker. It carries a random
	// suffix so a zombie process and its replacement never share one, which is
	// what lets the heartbeat renew leases in a single statement keyed on it.
	workerID string

	// now and jitter are the injection points for tests. Unexported because they
	// are not configuration — an application has no reason to replace either.
	now    func() time.Time
	jitter jitterFunc

	// stopClaiming ends polling, ticking and heartbeating; abortTasks cancels the
	// contexts of tasks already in flight. Two cancels, not one, because that
	// distinction IS the drain: Stop uses the first immediately and the second only
	// once the grace period is spent.
	stopClaiming context.CancelFunc
	abortTasks   context.CancelFunc
	done         chan struct{}

	// wake nudges an idle poller. Capacity one and never blocking, so enqueueing
	// from a handler cannot deadlock on a poller that is busy.
	wake chan struct{}

	// inFlight maps each execution this process is running to its handler's cancel.
	// Keyed by leaseHold rather than by task id: see that type for why one id is not
	// enough to identify an execution.
	mu       sync.Mutex
	inFlight map[leaseHold]context.CancelFunc
}

// New constructs the queue service. cfg may be nil; Start reports that as the
// missing-config error rather than degrading.
func New(cfg *Config, db *sqldb.DB, reg *Registry, log *slog.Logger, opts ...Option) *Service {
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		reg = NewRegistry()
	}
	s := &Service{
		cfg:      cfg,
		db:       db,
		reg:      reg,
		log:      log,
		now:      time.Now,
		jitter:   randomJitter,
		wake:     make(chan struct{}, 1),
		inFlight: map[leaseHold]context.CancelFunc{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start validates the configuration, migrates, syncs the code's cron plans into
// the database, and — unless concurrency is zero — launches the poller, the ticker
// and the heartbeat.
//
// It returns as soon as those goroutines exist, which is what app.Lifecycle's
// contract asks for: acquire process-lifetime resources and return. The resources
// here are the goroutines and the leases they hold. Blocking instead would force
// runServe to become an errgroup, rewriting the one file every component's wiring
// lives in for the benefit of this one.
//
// ctx is the STARTUP context and is used only for the work above. The goroutines
// get a context derived with context.WithoutCancel — see the comment at that line,
// which is the single most important detail in this file.
func (s *Service) Start(ctx context.Context) error {
	if s.cfg == nil {
		return errors.New("queue: service enabled but [queue] config section missing " +
			"(copy that section from config.example.yaml)")
	}
	if s.db == nil {
		return errors.New("queue: needs the db component (svc.DB is nil)")
	}
	if s.cfg.Concurrency == nil {
		return errors.New("queue: [queue] concurrency must be set " +
			"(0 disables the worker and keeps only the admin screens)")
	}
	if err := s.validateConfig(); err != nil {
		return err
	}

	loc, err := s.resolveLocation()
	if err != nil {
		return err
	}
	s.loc = loc

	id, err := newWorkerID()
	if err != nil {
		return err
	}
	s.workerID = id

	// A cron plan whose kind has no handler would fire forever into a queue nobody
	// drains. Caught here rather than at the first fire, because "startup refused"
	// is a legible failure and "tasks quietly accumulating" is not.
	if err := s.reg.Validate(); err != nil {
		return err
	}

	if s.migrateSchema {
		if err := migrate(ctx, s.db, s.migrationTable); err != nil {
			return err
		}
	}

	// Uses the startup context on purpose: this is bounded work that a startup
	// timeout should be able to interrupt.
	if err := s.syncSchedules(ctx); err != nil {
		return err
	}

	if s.Concurrency() == 0 {
		s.log.Info("queue: worker disabled (concurrency=0); admin screens only")
		return nil
	}

	// context.WithoutCancel is load-bearing. runServe passes cmd.Context(), which
	// main.go wired to signal.NotifyContext for SIGINT/SIGTERM. Deriving from it
	// would cancel every worker the instant Ctrl-C is pressed — before
	// eng.Serve() has returned and long before the deferred Stop runs — so
	// in-flight tasks would be killed outright instead of drained. The startup
	// context is for starting up; the run context outlives it and is ended only by
	// Stop.
	base := context.WithoutCancel(ctx)
	claimCtx, stopClaiming := context.WithCancel(base)
	taskCtx, abortTasks := context.WithCancel(base)
	s.stopClaiming, s.abortTasks = stopClaiming, abortTasks
	s.done = make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); s.pollLoop(claimCtx, taskCtx) }()
	go func() { defer wg.Done(); s.tickLoop(claimCtx) }()
	go func() { defer wg.Done(); s.heartbeatLoop(claimCtx) }()
	go func() { wg.Wait(); close(s.done) }()

	s.log.Info("queue: started",
		"worker", s.workerID,
		"concurrency", s.Concurrency(),
		"kinds", len(s.reg.Kinds()),
		"timezone", s.loc.String())
	return nil
}

// Stop drains: it stops claiming immediately, gives in-flight tasks up to
// ShutdownGrace to finish, then cancels their contexts and waits a little longer.
//
// The drain wait is ShutdownGrace whether or not the caller passed a deadline: a
// deadline is a ceiling on the whole call, not a replacement for the configured
// grace. Taking the deadline instead — which this once did, whenever one was set —
// made queue.shutdown_grace dead config in both call sites that set one, so Ctrl-C
// sat for the caller's two minutes and then logged an expiry quoting a grace period
// it had never used.
//
// A task interrupted by the grace period expiring is released back to pending with
// its attempt REFUNDED (see releaseTask). That is the only path that refunds, and
// the reason is knowledge: this process cancelled the handler itself and is about
// to be replaced by one that can pick the task up immediately, so charging the
// operator's deployment a retry charges it for nothing. The reaper cannot tell a
// deployment from a crash, so it never refunds.
func (s *Service) Stop(ctx context.Context) error {
	if s.stopClaiming == nil {
		return nil // never Started, or concurrency=0
	}
	s.stopClaiming()

	// time.Until, not the service clock: the caller's deadline is wall-clock, and so
	// is the timer context.WithTimeout arms. A deadline already past yields a
	// non-positive grace, which fires immediately — the correct reading of "you are
	// out of time".
	grace := s.ShutdownGrace()
	if deadline, ok := ctx.Deadline(); ok {
		grace = min(grace, time.Until(deadline))
	}
	graceCtx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	select {
	case <-s.done:
		return nil
	case <-graceCtx.Done():
	}

	s.log.Warn("queue: grace period expired, cancelling in-flight tasks",
		"grace", grace)
	s.abortTasks()
	select {
	case <-s.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("queue: workers did not exit within the grace period")
	}
}

// validateConfig checks the invariants between timing settings. They are refused
// rather than quietly corrected: each one, violated, produces a specific and
// confusing failure at runtime, and a corrected value would hide the fact that the
// operator asked for something impossible.
func (s *Service) validateConfig() error {
	if s.Concurrency() < 0 {
		return fmt.Errorf("queue: concurrency %d is negative", s.Concurrency())
	}
	// The one real invariant: the lease has to outlive more than one missed renewal,
	// or a single slow heartbeat hands a live task to the reaper.
	//
	// There is deliberately no check relating task_timeout to lease_ttl. The heartbeat
	// renews a running task's lease indefinitely, so the two are independent — see
	// Config.TaskTimeout for why tying them would be a mistake rather than a
	// safeguard.
	if lease, hb := s.LeaseTTL(), s.Heartbeat(); lease <= 2*hb {
		return fmt.Errorf("queue: lease_ttl (%s) must be more than twice heartbeat (%s), "+
			"or a single missed renewal loses the task", lease, hb)
	}
	if s.RetryMax() < s.RetryBase() {
		return fmt.Errorf("queue: retry_max (%s) is below retry_base (%s)",
			s.RetryMax(), s.RetryBase())
	}
	return nil
}

// resolveLocation resolves Timezone. A bad name stops startup; see the field's doc
// comment for why silence would be worse.
func (s *Service) resolveLocation() (*time.Location, error) {
	if s.cfg.Timezone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(s.cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("queue: timezone %q: %w", s.cfg.Timezone, err)
	}
	return loc, nil
}

// --- accessors: nil-safe, defaults applied here rather than in config.defaults() ---

// Concurrency is 0 when unset, which disables the worker. Start refuses an unset
// pointer, so reaching this with nil means a test constructed the service directly.
func (s *Service) Concurrency() int {
	if s.cfg == nil || s.cfg.Concurrency == nil {
		return 0
	}
	return *s.cfg.Concurrency
}

func (s *Service) PollInterval() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.PollInterval }, defaultPollInterval)
}
func (s *Service) TickInterval() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.TickInterval }, defaultTickInterval)
}
func (s *Service) LeaseTTL() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.LeaseTTL }, defaultLeaseTTL)
}
func (s *Service) Heartbeat() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.Heartbeat }, defaultHeartbeat)
}
func (s *Service) TaskTimeout() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.TaskTimeout }, defaultTaskTimeout)
}
func (s *Service) ShutdownGrace() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.ShutdownGrace }, defaultShutdownGrace)
}
func (s *Service) RetryBase() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.RetryBase }, defaultRetryBase)
}
func (s *Service) RetryMax() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.RetryMax }, defaultRetryMax)
}
func (s *Service) OrphanGrace() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.OrphanGrace }, defaultOrphanGrace)
}
func (s *Service) PruneInterval() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.PruneInterval }, defaultPruneInterval)
}

func (s *Service) RetentionSucceeded() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.RetentionSucceeded }, defaultRetentionOK)
}

func (s *Service) RetentionDead() time.Duration {
	return s.dur(func(c *Config) time.Duration { return c.RetentionDead }, defaultRetentionDead)
}

// MaxAttempts is the default ceiling for a kind that does not declare its own.
func (s *Service) MaxAttempts() int {
	if s.cfg == nil {
		return defaultMaxAttempts
	}
	return cmp.Or(s.cfg.MaxAttempts, defaultMaxAttempts)
}

// AdminPageSize is rows per page in the admin task list.
func (s *Service) AdminPageSize() int {
	if s.cfg == nil {
		return defaultAdminPageSize
	}
	return cmp.Or(s.cfg.AdminPageSize, defaultAdminPageSize)
}

// dur reads one duration field with its default, staying safe for a nil cfg so
// tests may skip Start. A negative value is treated as unset rather than honoured:
// every duration here means "how long", and none of them has a meaning below zero.
func (s *Service) dur(get func(*Config) time.Duration, def time.Duration) time.Duration {
	if s.cfg == nil {
		return def
	}
	if d := get(s.cfg); d > 0 {
		return d
	}
	return def
}

// --- exported surface the rest of the application uses ---

// ParseCron parses a cron expression, and is the only parser in the system.
//
// Exposed as a method so the admin form's validation rule and its "next five runs"
// preview go through exactly what the scheduler runs on. Validating with a second
// implementation is precisely how "saved fine, never fires" happens.
func (s *Service) ParseCron(expr string) (Schedule, error) { return ParseCron(expr) }

// Kinds returns the handler kinds registered in this process, sorted.
//
// The admin screens use it for the kind filter and to flag a task whose handler is
// gone; the claim query uses it to avoid taking work this process cannot run.
func (s *Service) Kinds() []string { return s.reg.Kinds() }

// Location is the zone cron expressions are evaluated in.
func (s *Service) Location() *time.Location {
	if s.loc == nil {
		return time.Local
	}
	return s.loc
}

// EnqueueOption tunes one Enqueue call.
type EnqueueOption func(*enqueueParams)

// At schedules the task for a specific time. A time in the past is the same as now.
func At(t time.Time) EnqueueOption {
	return func(p *enqueueParams) { p.RunAt = t.UnixNano() }
}

// After schedules the task for d from now — the delayed case. Resolved against the
// service clock at Enqueue time.
func After(d time.Duration) EnqueueOption {
	return func(p *enqueueParams) { p.delay = d }
}

// MaxAttempts overrides the ceiling for this task only.
func MaxAttempts(n int) EnqueueOption {
	return func(p *enqueueParams) { p.MaxAttempts = n }
}

// Priority raises or lowers this task against others that are already due.
func Priority(n int) EnqueueOption {
	return func(p *enqueueParams) { p.Priority = n }
}

// Unique makes the task idempotent under key: a second Enqueue with the same key
// fails rather than duplicating.
//
// The uniqueness is PERMANENT, not "unique among unfinished tasks" — MySQL has no
// partial index, so the portable constraint is the strong one. Include a time
// bucket in the key when the intent is "at most once per hour" rather than "at most
// once ever".
func Unique(key string) EnqueueOption {
	return func(p *enqueueParams) { p.UniqueKey = key }
}

// Enqueue adds a task and returns its id.
//
// Safe to call from an HTTP handler and from inside another task's handler. It also
// nudges this process's poller, so a task enqueued with no delay starts without
// waiting out a poll interval.
func (s *Service) Enqueue(ctx context.Context, kind string, payload any, opts ...EnqueueOption) (int64, error) {
	raw, err := marshalPayload(payload)
	if err != nil {
		return 0, err
	}

	now := s.now()
	p := s.newTaskParams(kind, raw, now.UnixNano())
	for _, opt := range opts {
		opt(&p)
	}
	if p.delay > 0 {
		p.RunAt = now.Add(p.delay).UnixNano()
	}
	// Again after the options, which are allowed to say MaxAttempts(0) — defending
	// the option is a different job from supplying the default.
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = s.MaxAttempts()
	}

	id, err := insertTask(ctx, s.db, p, now.UnixNano())
	if err != nil {
		return 0, err
	}
	s.nudge()
	return id, nil
}

// newTaskParams starts a task of this kind from what its registration declared,
// with the service-wide fallback filled in where the registration was silent.
//
// Every producer of a queue_tasks row goes through this — Enqueue, the cron
// scheduler and a hand-fired plan — because "what does a task of this kind default
// to" is a property of the kind, not of the entry point that happened to create it.
// It was not always: the two cron producers each filled max_attempts in by hand and
// never looked at priority or timeout at all, so WithTimeout and WithPriority were
// documented as per-kind and silently dropped for every cron-fired task. A fourth
// HandleOption would have needed remembering in three places.
//
// An unregistered kind is allowed: the task simply waits (visibly) until a process
// that handles it exists, which is what makes deploying a producer before its
// consumer safe.
func (s *Service) newTaskParams(kind string, payload []byte, runAt int64) enqueueParams {
	p := enqueueParams{Kind: kind, Payload: payload, RunAt: runAt}
	if e, ok := s.reg.lookup(kind); ok {
		p.MaxAttempts = e.maxAttempts
		p.Priority = e.priority
		p.TimeoutMS = int(e.timeout / time.Millisecond)
	}
	p.MaxAttempts = cmp.Or(p.MaxAttempts, s.MaxAttempts())
	return p
}

// nudge wakes an idle poller without blocking. A full channel already means "there
// is a wake-up pending", so dropping the signal loses nothing.
func (s *Service) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// newWorkerID identifies this process: host, pid, and a random suffix.
//
// The suffix is what makes the id unique across restarts, and that is what lets the
// heartbeat renew all of this process's leases with a single statement keyed on
// worker. Without it a restarted process on the same host with a recycled pid could
// renew the leases of the instance it replaced.
func newWorkerID() (string, error) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("queue: generate worker id: %w", err)
	}
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b[:])), nil
}
