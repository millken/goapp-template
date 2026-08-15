// Package tasks is the single registration point for this application's background
// work: every handler kind, and every cron plan's default expression.
//
// It has the same standing as internal/controller/mount_gen.go — one wiring table
// that both composition roots read, so `serve` (with an embedded worker) and
// `myapp queue worker` cannot drift apart. If a kind is missing from one of them,
// tasks of that kind sit unclaimed with no worker able to run them, so there is
// exactly one list and both call it.
//
// Why it is not inside internal/service/queue: a handler needs the application's
// services, and queue must not import internal/app (internal/config imports queue,
// so that edge would close a cycle). The handlers here close over *app.Services
// instead, which keeps the dependency pointing one way — tasks → {queue, app} — and
// leaves queue knowing nothing about the application it serves.
//
// Business logic does not belong in this file. A handler here should be a few lines
// that unpack a payload and call into internal/service/…, exactly as a controller is
// a few lines that unpack a request. What lives here is the wiring: which kind, which
// schedule, which ceiling.
package tasks

import (
	"context"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/service/queue"
)

// Register declares every task kind and cron plan.
//
// Called by each composition root after the service container is filled and BEFORE
// queue.Service.Start, so a handler must not dereference anything on svc at
// registration time. Reading svc inside a handler body is fine and is the point —
// by the time a handler runs, Start has completed and every field is populated,
// including svc.Queue for a handler that enqueues follow-up work.
//
// Registration panics on a wiring mistake (an illegal kind, a duplicate, an
// unparseable expression). That is deliberate: the only possible source is this file,
// and the alternatives are an error nobody can report from here, or a silently
// replaced handler.
func Register(r *queue.Registry, svc *app.Services) {
	// tasks:begin
	//
	// The two entries below are examples. Delete them — they exist so a freshly
	// generated project has something to see on /admin/task and /admin/cron, and a
	// shape to copy. Same standing as frontend/pages/Home.vue and the "/" route in
	// internal/controller/site.

	// A one-shot task, and the shape most handlers take: a typed payload via
	// queue.JSON, so the handler receives a struct instead of bytes. A payload that
	// does not decode is reported as permanent — the bytes were fixed when the task
	// was enqueued, so retrying cannot make them parse.
	r.Handle("example:echo", queue.JSON(func(ctx context.Context, arg echoArg) error {
		svc.Log.Info("queue example: echo", "echoed", arg.Message)
		return nil
	}))

	// A periodic task. HandleCron registers the handler and a plan that fires it,
	// using the kind as the plan's name.
	//
	// The expression here is the DEFAULT, not the last word: it is written to
	// queue_schedules.code_spec, and an operator may change the live expression from
	// /admin/cron. A later deployment that changes this line only moves the stored
	// expression if nobody has edited it — see queue's syncSchedules.
	r.HandleCron("example:heartbeat", "@every 5m",
		func(ctx context.Context, t *queue.Task) error {
			svc.Log.Info("queue example: heartbeat", "attempt", t.Attempt)
			return nil
		},
		// One attempt: a heartbeat that missed its slot has nothing to catch up on,
		// and the next occurrence is along in five minutes.
		queue.WithMaxAttempts(1),
		queue.WithTimeout(30*time.Second),
	)
	// tasks:end
}

// echoArg is the example handler's payload. A handler's argument type belongs next to
// its registration when it is this small; anything real belongs with the service that
// owns it.
type echoArg struct {
	Message string `json:"message"`
}
