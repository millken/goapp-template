package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// ErrPermanent marks a failure that retrying cannot fix. A handler wraps it —
// `fmt.Errorf("bad shipping address: %w", queue.ErrPermanent)` — and the task
// goes straight to dead with no backoff.
//
// It is a sentinel rather than an error type because what the queue needs to know
// is one bit, and errors.Is composes: a handler can wrap it through as many
// layers as it likes.
//
// The distinction it draws is "business refusal" versus "transient failure", and
// it matters in both directions. Without it, a payload the handler will never
// accept burns max_attempts slots and an hour of backoff to reach the same
// conclusion it reached the first time. Used too eagerly, a database hiccup
// becomes a dead task that needs a human. The rule: permanent means "the input is
// wrong", not "the world is broken right now".
var ErrPermanent = errors.New("queue: permanent failure")

// Task is what a handler is given: the payload, plus the two facts about the
// attempt that can legitimately change behaviour.
//
// The whole struct rather than just the payload, because a handler wants the id in
// its log lines, and because "this is attempt 3 of 3" is actionable — the last
// attempt is where you send the alert instead of silently returning an error
// again.
type Task struct {
	ID      int64
	Kind    string
	Payload []byte

	// Attempt is 1-based and counts this attempt. It equals the queue_tasks row's
	// attempts column, which is incremented at claim time — so a handler that
	// crashed the process still consumed one.
	Attempt     int
	MaxAttempts int

	// ScheduleID is the cron plan that produced this task, or 0 for a task that
	// was enqueued directly.
	ScheduleID int64
}

// Last reports whether this is the final attempt, so a handler can escalate
// instead of failing quietly one more time.
func (t *Task) Last() bool { return t.MaxAttempts > 0 && t.Attempt >= t.MaxAttempts }

// HandlerFunc runs one task. Returning nil is success; any other error is a
// failure that will be retried until max_attempts, unless it wraps ErrPermanent.
//
// ctx carries the per-task timeout and is cancelled when the process is shutting
// down or the task is cancelled from the admin area. A handler that ignores it
// still works — the shutdown path has its own grace period — but it holds up
// deployments.
type HandlerFunc func(ctx context.Context, t *Task) error

// HandleOption tunes one registration.
type HandleOption func(*handlerEntry)

// WithMaxAttempts overrides the configured default for this kind. Recorded onto
// each task at enqueue time, so changing it later does not disturb work already
// queued.
func WithMaxAttempts(n int) HandleOption {
	return func(e *handlerEntry) { e.maxAttempts = n }
}

// WithTimeout overrides the configured task_timeout for this kind. Must stay
// below lease_ttl or the reaper would reclaim a task that is still running; the
// heartbeat renews the lease of a long task, so the ceiling is on one attempt's
// wall time, not on the lease.
func WithTimeout(d time.Duration) HandleOption {
	return func(e *handlerEntry) { e.timeout = d }
}

// WithPriority sets the default priority for tasks of this kind. Higher runs
// first among tasks that are already due.
func WithPriority(p int) HandleOption {
	return func(e *handlerEntry) { e.priority = p }
}

type handlerEntry struct {
	fn          HandlerFunc
	maxAttempts int
	timeout     time.Duration
	priority    int
}

// cronEntry is one registered plan, as the code declared it. spec is kept as text
// as well as parsed because it is what gets written to queue_schedules.code_spec —
// the base of the three-way merge in syncSchedules.
type cronEntry struct {
	name        string
	spec        string
	schedule    Schedule
	kind        string
	payload     []byte
	maxAttempts int
}

// Registry maps kinds to handlers and declares the cron plans.
//
// Written only during startup wiring and read-only afterwards — the same contract
// as Admin.AddMenuItem and Admin.recordPermission, and the reason there is no
// lock. A Register call after Start would be a data race, so the API offers no way
// to reach it: the composition root builds the registry, hands it to New, and
// drops its own reference to the mutating half.
type Registry struct {
	handlers map[string]handlerEntry
	crons    []cronEntry

	// complete records that this registry is the whole application's, not a
	// filtered subset.
	//
	// Two behaviours depend on it, and both are destructive when it is wrong:
	// marking a schedule present=0 because its name is not registered here, and
	// declaring an orphan task dead because its kind is not registered here. A
	// worker started with a subset of kinds would do both to work it simply
	// cannot see. There is no --kinds flag today, so this is always true — the
	// field exists so the flag cannot be added without confronting the question.
	complete bool
}

// NewRegistry returns an empty registry marked complete.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]handlerEntry{}, complete: true}
}

// kindRe keeps a kind to characters that survive a log line, a URL query
// parameter and a SQL string literal unremarkably. The colon is allowed because
// "mail:welcome" is the natural way to namespace these.
var kindRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]*$`)

// Handle registers the handler for kind.
//
// Panics on an illegal kind or a duplicate registration. These can only come from
// wiring code in this repository — the same class as the panic in Admin.Resource —
// and both alternatives are worse: a rejected kind returned as an error would be
// ignored by wiring that has nowhere to report it, and a silently overwritten
// handler means the queue runs code nobody expects it to.
func (r *Registry) Handle(kind string, fn HandlerFunc, opts ...HandleOption) {
	if !kindRe.MatchString(kind) {
		panic("queue: Handle: illegal kind " + strconv.Quote(kind) +
			" (want ^[a-z0-9][a-z0-9_.:-]*$)")
	}
	if fn == nil {
		panic("queue: Handle: nil handler for kind " + strconv.Quote(kind))
	}
	if _, dup := r.handlers[kind]; dup {
		panic("queue: Handle: kind " + strconv.Quote(kind) + " is already registered")
	}
	e := handlerEntry{fn: fn}
	for _, opt := range opts {
		opt(&e)
	}
	r.handlers[kind] = e
}

// Cron declares a periodic plan named name that enqueues kind with payload.
//
// name is the plan's stable identity — it is what queue_schedules rows are matched
// on across restarts, and therefore what carries an operator's expression edit
// forward. It is declared rather than derived from kind so that two plans can run
// the same handler on different schedules, and so renaming a handler does not
// silently create a second plan and abandon the first with present=0.
//
// payload is marshalled here so a bad one panics at wiring time instead of failing
// on every fire. The expression is parsed here for the same reason.
func (r *Registry) Cron(name, spec, kind string, payload any, opts ...HandleOption) {
	if !kindRe.MatchString(name) {
		panic("queue: Cron: illegal name " + strconv.Quote(name) +
			" (want ^[a-z0-9][a-z0-9_.:-]*$)")
	}
	if slices.ContainsFunc(r.crons, func(c cronEntry) bool { return c.name == name }) {
		panic("queue: Cron: name " + strconv.Quote(name) + " is already registered")
	}
	sched, err := ParseCron(spec)
	if err != nil {
		panic("queue: Cron: " + strconv.Quote(name) + ": " + err.Error())
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		panic("queue: Cron: " + strconv.Quote(name) + ": " + err.Error())
	}
	var e handlerEntry
	for _, opt := range opts {
		opt(&e)
	}
	r.crons = append(r.crons, cronEntry{
		name: name, spec: spec, schedule: sched,
		kind: kind, payload: raw, maxAttempts: e.maxAttempts,
	})
}

// HandleCron registers a handler and a plan that fires it, using kind as the
// plan's name. The common case, where a periodic job's handler exists only to be
// scheduled.
func (r *Registry) HandleCron(kind, spec string, fn HandlerFunc, opts ...HandleOption) {
	r.Handle(kind, fn, opts...)
	r.Cron(kind, spec, kind, nil, opts...)
}

// Kinds returns the registered kinds, sorted.
//
// This is what the claim query filters on, and what the admin screens use to
// offer a kind filter and to flag a task whose handler is gone. Sorted so the
// claim SQL — whose placeholder count comes from this slice — is stable, which
// keeps it cacheable by the driver.
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.handlers))
	for kind := range r.handlers {
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

// lookup returns the handler entry for kind.
func (r *Registry) lookup(kind string) (handlerEntry, bool) {
	e, ok := r.handlers[kind]
	return e, ok
}

// cronEntries returns the registered plans in declaration order. Used by
// syncSchedules to tell a plan the code still declares from one it no longer
// does — the latter becomes present=0 rather than being deleted.
func (r *Registry) cronEntries() []cronEntry { return slices.Clone(r.crons) }

// Validate reports the wiring mistakes that a panic at registration time cannot
// catch, because they are about the relationship between two registrations rather
// than about one of them.
//
// Currently one: a plan whose kind has no handler. That plan would fire on
// schedule, enqueue a task, and the task would sit in pending forever because no
// worker claims a kind it cannot run.
//
// Start calls it, so it is a startup failure. It is exported so internal/tasks can
// call it from a test — which turns the same mistake into a build failure, and a
// wiring mistake in a wiring file should not need a deployment to surface.
func (r *Registry) Validate() error {
	for _, c := range r.crons {
		if _, ok := r.handlers[c.kind]; !ok {
			return fmt.Errorf("queue: cron %q fires kind %q, which has no handler "+
				"(the plan would enqueue tasks nothing can run)", c.name, c.kind)
		}
	}
	return nil
}

// marshalPayload turns a handler argument into the bytes stored in the payload
// column. nil becomes an empty JSON object rather than an empty string or SQL
// NULL: the column is NOT NULL with no default, and a handler decoding "{}" into
// a struct of zero values is friendlier than one that has to special-case "".
//
// []byte and string pass through unchanged, so a caller holding pre-encoded JSON
// is not double-encoded into a quoted string.
func marshalPayload(payload any) ([]byte, error) {
	switch v := payload.(type) {
	case nil:
		return []byte("{}"), nil
	case []byte:
		if len(v) == 0 {
			return []byte("{}"), nil
		}
		return v, nil
	case string:
		if v == "" {
			return []byte("{}"), nil
		}
		return []byte(v), nil
	case json.RawMessage:
		if len(v) == 0 {
			return []byte("{}"), nil
		}
		return v, nil
	default:
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("queue: encode payload: %w", err)
		}
		return raw, nil
	}
}

// JSON adapts a handler that wants its payload decoded into T.
//
// The registry itself stays monomorphic — its values are looked up by a string
// read out of the database, so the map cannot be generic. Generics help only at
// the registration edge, which is exactly what this is:
//
//	r.Handle("mail:welcome", queue.JSON(func(ctx context.Context, arg welcomeArg) error {
//	    return mailer.SendWelcome(ctx, arg.UserID)
//	}))
//
// A payload that does not decode is wrapped in ErrPermanent: the bytes were fixed
// when the task was enqueued, so no amount of retrying will make them parse. That
// failure lands in the attempts log with the decoder's message, which is what
// makes it debuggable from the admin screen.
func JSON[T any](fn func(ctx context.Context, arg T) error) HandlerFunc {
	return func(ctx context.Context, t *Task) error {
		var arg T
		if err := json.Unmarshal(t.Payload, &arg); err != nil {
			return fmt.Errorf("queue: decode payload for %s: %v: %w", t.Kind, err, ErrPermanent)
		}
		return fn(ctx, arg)
	}
}
