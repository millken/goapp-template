package queue

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func noop(context.Context, *Task) error { return nil }

func TestRegistry_HandleAndKinds(t *testing.T) {
	r := NewRegistry()
	r.Handle("b:second", noop)
	r.Handle("a:first", noop)
	r.Handle("mail.welcome", noop)

	// Sorted, because the claim query's placeholder list is built from this and a
	// stable statement is one the driver can cache.
	want := []string{"a:first", "b:second", "mail.welcome"}
	if got := r.Kinds(); !slices.Equal(got, want) {
		t.Errorf("Kinds() = %v, want %v", got, want)
	}

	if _, ok := r.lookup("a:first"); !ok {
		t.Error("lookup missed a registered kind")
	}
	if _, ok := r.lookup("nope"); ok {
		t.Error("lookup found an unregistered kind")
	}
}

func TestNewRegistry_IsComplete(t *testing.T) {
	// present=0 reclamation and orphan death both depend on this being honest.
	if !NewRegistry().complete {
		t.Error("a fresh registry must be complete: it is the whole application's")
	}
}

func TestRegistry_KindsIsEmptyNotNilForAFreshRegistry(t *testing.T) {
	if got := NewRegistry().Kinds(); got == nil {
		t.Error("Kinds() returned nil; the claim path checks len() and must not see nil vs empty differently")
	}
}

// Wiring mistakes panic rather than returning an error. They can only come from
// code in this repository, and the alternatives are worse: an error has nowhere to
// be reported from a Register call, and a silently replaced handler means the queue
// runs code nobody expects.
func TestRegistry_PanicsOnWiringMistakes(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Registry)
		want string
	}{
		{"illegal kind: uppercase", func(r *Registry) { r.Handle("Mail", noop) }, "illegal kind"},
		{"illegal kind: leading dash", func(r *Registry) { r.Handle("-mail", noop) }, "illegal kind"},
		{"illegal kind: space", func(r *Registry) { r.Handle("send mail", noop) }, "illegal kind"},
		{"illegal kind: empty", func(r *Registry) { r.Handle("", noop) }, "illegal kind"},
		{"illegal kind: slash", func(r *Registry) { r.Handle("mail/welcome", noop) }, "illegal kind"},
		{"nil handler", func(r *Registry) { r.Handle("mail", nil) }, "nil handler"},
		{"duplicate kind", func(r *Registry) {
			r.Handle("mail", noop)
			r.Handle("mail", noop)
		}, "already registered"},
		{"illegal cron name", func(r *Registry) { r.Cron("Nightly", "@daily", "mail", nil) }, "illegal name"},
		{"duplicate cron name", func(r *Registry) {
			r.Cron("nightly", "@daily", "mail", nil)
			r.Cron("nightly", "@hourly", "mail", nil)
		}, "already registered"},
		{"unparseable spec", func(r *Registry) { r.Cron("nightly", "0 99 * * *", "mail", nil) }, "hour"},
		{"unmarshalable payload", func(r *Registry) {
			r.Cron("nightly", "@daily", "mail", func() {})
		}, "encode payload"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				rec := recover()
				if rec == nil {
					t.Fatalf("expected a panic containing %q", c.want)
				}
				msg, _ := rec.(string)
				if !strings.Contains(msg, c.want) {
					t.Errorf("panic = %v, want it to mention %q", rec, c.want)
				}
			}()
			c.fn(NewRegistry())
		})
	}
}

func TestRegistry_HandleOptions(t *testing.T) {
	r := NewRegistry()
	r.Handle("slow", noop, WithMaxAttempts(7), WithTimeout(90*time.Second), WithPriority(5))

	e, ok := r.lookup("slow")
	if !ok {
		t.Fatal("kind not registered")
	}
	if e.maxAttempts != 7 || e.timeout != 90*time.Second || e.priority != 5 {
		t.Errorf("entry = %+v, want maxAttempts 7, timeout 90s, priority 5", e)
	}

	// Unset options stay zero, which is how the accessors know to fall back to the
	// configured defaults rather than to a value this file invented.
	r.Handle("plain", noop)
	plain, _ := r.lookup("plain")
	if plain.maxAttempts != 0 || plain.timeout != 0 || plain.priority != 0 {
		t.Errorf("unset options = %+v, want all zero so config supplies the defaults", plain)
	}
}

func TestRegistry_Cron(t *testing.T) {
	r := NewRegistry()
	r.Handle("report", noop)
	r.Cron("report:nightly", "0 3 * * *", "report", map[string]string{"scope": "all"},
		WithMaxAttempts(2))

	entries := r.cronEntries()
	if len(entries) != 1 {
		t.Fatalf("got %d cron entries, want 1", len(entries))
	}
	c := entries[0]
	if c.name != "report:nightly" || c.kind != "report" || c.spec != "0 3 * * *" {
		t.Errorf("entry = %+v", c)
	}
	if c.maxAttempts != 2 {
		t.Errorf("maxAttempts = %d, want 2", c.maxAttempts)
	}
	if string(c.payload) != `{"scope":"all"}` {
		t.Errorf("payload = %s", c.payload)
	}
	// The spec is parsed at registration, so the schedule is usable without
	// re-parsing — and an unparseable one has already panicked.
	if _, err := c.schedule.Next(time.Now()); err != nil {
		t.Errorf("stored schedule is unusable: %v", err)
	}
}

// HandleCron is the common shape: one handler that exists to be scheduled. It must
// register both halves, and use the kind as the plan name.
func TestRegistry_HandleCron(t *testing.T) {
	r := NewRegistry()
	r.HandleCron("cleanup", "0 4 * * *", noop, WithMaxAttempts(1))

	if _, ok := r.lookup("cleanup"); !ok {
		t.Error("HandleCron did not register the handler")
	}
	entries := r.cronEntries()
	if len(entries) != 1 || entries[0].name != "cleanup" || entries[0].kind != "cleanup" {
		t.Fatalf("cron entries = %+v, want one named and kinded 'cleanup'", entries)
	}
	if entries[0].maxAttempts != 1 {
		t.Errorf("options did not reach the plan: %+v", entries[0])
	}
}

// The one wiring mistake a per-registration panic cannot catch, because it is
// about the relationship between two registrations. Such a plan fires on schedule
// and enqueues tasks that no worker will ever claim — a queue that grows quietly.
func TestRegistry_ValidateRejectsACronWithNoHandler(t *testing.T) {
	r := NewRegistry()
	r.Cron("nightly", "@daily", "does-not-exist", nil)

	err := r.Validate()
	if err == nil {
		t.Fatal("validate accepted a plan whose kind has no handler")
	}
	for _, want := range []string{"nightly", "does-not-exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}

	r.Handle("does-not-exist", noop)
	if err := r.Validate(); err != nil {
		t.Errorf("validate still fails once the handler exists: %v", err)
	}
}

func TestRegistry_ValidateAcceptsAnEmptyRegistry(t *testing.T) {
	if err := NewRegistry().Validate(); err != nil {
		t.Errorf("an empty registry is valid (a build with no tasks): %v", err)
	}
}

func TestMarshalPayload(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		// The column is NOT NULL with no default, so "nothing" has to become
		// something. "{}" decodes into a struct of zero values, which is friendlier
		// than "" failing every decoder.
		{"nil", nil, "{}"},
		{"empty bytes", []byte{}, "{}"},
		{"empty string", "", "{}"},
		// Pre-encoded JSON passes through rather than being quoted into a string,
		// so a caller holding bytes is not double-encoded.
		{"raw bytes", []byte(`{"a":1}`), `{"a":1}`},
		{"raw string", `{"a":1}`, `{"a":1}`},
		{"struct", struct {
			A int `json:"a"`
		}{1}, `{"a":1}`},
		{"map", map[string]int{"a": 1}, `{"a":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := marshalPayload(c.in)
			if err != nil {
				t.Fatalf("marshalPayload: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("marshalPayload = %s, want %s", got, c.want)
			}
		})
	}

	if _, err := marshalPayload(func() {}); err == nil {
		t.Error("marshalPayload accepted a func")
	}
}

func TestJSON_DecodesIntoTheHandlersType(t *testing.T) {
	type arg struct {
		UserID int    `json:"user_id"`
		Note   string `json:"note"`
	}
	var got arg
	h := JSON(func(_ context.Context, a arg) error {
		got = a
		return nil
	})

	err := h(context.Background(), &Task{Kind: "t", Payload: []byte(`{"user_id":7,"note":"hi"}`)})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got.UserID != 7 || got.Note != "hi" {
		t.Errorf("decoded %+v", got)
	}
}

// A payload that does not decode is permanent: the bytes were fixed when the task
// was enqueued, so retrying cannot make them parse. Retrying anyway would spend
// max_attempts and an hour of backoff to reach the first attempt's conclusion.
func TestJSON_UndecodablePayloadIsPermanent(t *testing.T) {
	h := JSON(func(context.Context, struct{ A int }) error {
		t.Fatal("handler ran despite an undecodable payload")
		return nil
	})

	err := h(context.Background(), &Task{Kind: "mail:welcome", Payload: []byte(`not json`)})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error %v does not wrap ErrPermanent", err)
	}
	// The kind and the decoder's own message have to survive into the attempts
	// log, or the admin screen shows a failure with nothing to act on.
	if !strings.Contains(err.Error(), "mail:welcome") {
		t.Errorf("error %q should name the kind", err)
	}
}

func TestJSON_PropagatesTheHandlersError(t *testing.T) {
	sentinel := errors.New("boom")
	h := JSON(func(context.Context, map[string]any) error { return sentinel })

	err := h(context.Background(), &Task{Payload: []byte(`{}`)})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want the handler's own error", err)
	}
	if errors.Is(err, ErrPermanent) {
		t.Error("a handler's plain error must not become permanent")
	}
}

func TestTask_Last(t *testing.T) {
	cases := []struct {
		attempt, max int
		want         bool
	}{
		{1, 3, false},
		{2, 3, false},
		{3, 3, true},
		{4, 3, true}, // a raised ceiling could put attempts past max
		{1, 1, true},
		{1, 0, false}, // no ceiling means never "last"
	}
	for _, c := range cases {
		task := &Task{Attempt: c.attempt, MaxAttempts: c.max}
		if got := task.Last(); got != c.want {
			t.Errorf("Task{Attempt: %d, MaxAttempts: %d}.Last() = %v, want %v",
				c.attempt, c.max, got, c.want)
		}
	}
}
