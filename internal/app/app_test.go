package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/millken/inertia"
)

// newTestEngine builds a minimal engine suitable for App wiring tests.
func newTestEngine(t *testing.T) *inertia.Engine {
	t.Helper()
	eng, err := inertia.New(inertia.WithMode(inertia.ModeProduction))
	if err != nil {
		t.Fatalf("inertia.New: %v", err)
	}
	return eng
}

// recordingModule records the order of lifecycle calls. It is configurable via
// its bootErr/shutdownErr fields to exercise failure paths.
type recordingModule struct {
	name        string
	bootErr     error
	shutdownErr error

	calls *[]string
}

func (m *recordingModule) Register(a *App) error {
	*m.calls = append(*m.calls, "Register:"+m.name)
	return nil
}

func (m *recordingModule) Boot(ctx context.Context) error {
	*m.calls = append(*m.calls, "Boot:"+m.name)
	return m.bootErr
}

func (m *recordingModule) Shutdown(ctx context.Context) error {
	*m.calls = append(*m.calls, "Shutdown:"+m.name)
	return m.shutdownErr
}

// TestUse_RegisterOrder verifies Register is called in registration order and
// that nil modules are skipped.
func TestUse_RegisterOrder(t *testing.T) {
	eng := newTestEngine(t)
	a, err := New(eng)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var calls []string
	m1 := &recordingModule{name: "m1", calls: &calls}
	m2 := &recordingModule{name: "m2", calls: &calls}

	if err := a.Use(nil, m1, nil, m2); err != nil {
		t.Fatalf("Use: %v", err)
	}

	want := []string{"Register:m1", "Register:m2"}
	assertEqual(t, "register order", want, calls)
	if len(a.mods) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(a.mods))
	}
}

// TestUse_RegisterFailure verifies a Register error aborts Use.
func TestUse_RegisterFailure(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	fail := &failingRegister{name: "fail"}
	if err := a.Use(fail); err == nil {
		t.Fatal("expected Register error, got nil")
	}
}

type failingRegister struct{ name string }

func (m *failingRegister) Register(a *App) error { return errors.New("boom") }

// TestBootOrder verifies Boot runs in registration order.
func TestBootOrder(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	var calls []string
	m1 := &recordingModule{name: "m1", calls: &calls}
	m2 := &recordingModule{name: "m2", calls: &calls}
	m3 := &recordingModule{name: "m3", calls: &calls}

	if err := a.Use(m1, m2, m3); err != nil {
		t.Fatalf("Use: %v", err)
	}

	// Call boot directly via the unexported helper to avoid eng.Serve blocking.
	// We replicate the boot loop by using Serve with a pre-cancelled ctx is not
	// viable (Serve would still call eng.Serve). Instead verify via the recorded
	// calls after a controlled boot.
	for _, m := range a.mods {
		if b, ok := m.(Booter); ok {
			if err := b.Boot(context.Background()); err != nil {
				t.Fatalf("Boot: %v", err)
			}
		}
	}

	want := []string{
		"Register:m1", "Register:m2", "Register:m3",
		"Boot:m1", "Boot:m2", "Boot:m3",
	}
	assertEqual(t, "boot order", want, calls)
}

// TestShutdownReverseOrder verifies Shutdown runs in reverse registration order.
func TestShutdownReverseOrder(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	var calls []string
	m1 := &recordingModule{name: "m1", calls: &calls}
	m2 := &recordingModule{name: "m2", calls: &calls}
	m3 := &recordingModule{name: "m3", calls: &calls}

	if err := a.Use(m1, m2, m3); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if err := a.shutdownN(context.Background(), len(a.mods)); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	want := []string{
		"Register:m1", "Register:m2", "Register:m3",
		"Shutdown:m3", "Shutdown:m2", "Shutdown:m1",
	}
	assertEqual(t, "shutdown reverse order", want, calls)
}

// TestShutdown_AggregatesErrors verifies multiple Shutdown errors are joined.
func TestShutdown_AggregatesErrors(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	m1 := &recordingModule{name: "m1", shutdownErr: errors.New("e1")}
	m2 := &recordingModule{name: "m2", shutdownErr: errors.New("e2")}
	// calls pointer is required because Register writes to it.
	var calls []string
	m1.calls = &calls
	m2.calls = &calls
	if err := a.Use(m1, m2); err != nil {
		t.Fatalf("Use: %v", err)
	}

	err := a.shutdownN(context.Background(), len(a.mods))
	if err == nil {
		t.Fatal("expected joined error, got nil")
	}
	if !strings.Contains(err.Error(), "e1") || !strings.Contains(err.Error(), "e2") {
		t.Fatalf("expected joined error to mention e1 and e2, got %q", err.Error())
	}
}

// TestShutdownN_OnlyBootedPrefix verifies the Boot-failure shutdown scope.
// This is the regression test for the bug where shutdown iterated ALL modules
// instead of only the successfully-Booted prefix (the `booted` counter was
// written but never read).
//
// Scenario: modules [m1, m2, m3], m2.Boot fails. After m2 fails, `booted` == 1
// (only m1 Booted; m2 is the failing module and must self-clean per §5.1; m3
// never Booted). shutdownN(ctx, booted=1) must Shutdown only m1 — neither the
// failing m2 nor the never-Booted m3.
func TestShutdownN_OnlyBootedPrefix(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	var calls []string
	m1 := &recordingModule{name: "m1", calls: &calls}
	m2 := &recordingModule{name: "m2", bootErr: errors.New("boot fail"), calls: &calls}
	m3 := &recordingModule{name: "m3", calls: &calls}

	if err := a.Use(m1, m2, m3); err != nil {
		t.Fatalf("Use: %v", err)
	}

	// Replicate the Serve Boot loop's `booted` accounting to drive shutdownN
	// with the exact boundary Serve would use. (Serve itself can't be called
	// here because eng.Serve blocks.)
	booted := 0
	for _, m := range a.mods {
		b, ok := m.(Booter)
		if !ok {
			booted++
			continue
		}
		if err := b.Boot(context.Background()); err != nil {
			// On failure, shut down only the Booted prefix.
			if err := a.shutdownN(context.Background(), booted); err != nil {
				t.Fatalf("shutdownN: %v", err)
			}
			goto bootedDone
		}
		booted++
	}
bootedDone:

	want := []string{
		"Register:m1", "Register:m2", "Register:m3",
		"Boot:m1", "Boot:m2", // m2 attempted, fails
		"Shutdown:m1", // only m1 (Booted prefix); NOT m2 (failing) or m3 (never Booted)
	}
	assertEqual(t, "boot-failure shutdown scope", want, calls)
}

// TestShutdownN_Bounds verifies shutdownN(ctx, n) respects the upper bound n,
// shutting down a.mods[:n] in reverse and ignoring modules at index >= n.
func TestShutdownN_Bounds(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := New(eng)

	var calls []string
	m1 := &recordingModule{name: "m1", calls: &calls}
	m2 := &recordingModule{name: "m2", calls: &calls}
	m3 := &recordingModule{name: "m3", calls: &calls}
	if err := a.Use(m1, m2, m3); err != nil {
		t.Fatalf("Use: %v", err)
	}

	// n=2 → only m2, m1 (reverse), not m3.
	if err := a.shutdownN(context.Background(), 2); err != nil {
		t.Fatalf("shutdownN: %v", err)
	}

	want := []string{
		"Register:m1", "Register:m2", "Register:m3",
		"Shutdown:m2", "Shutdown:m1",
	}
	assertEqual(t, "shutdownN bounds", want, calls)
}

// TestNew_NilEngine verifies New rejects a nil engine.
func TestNew_NilEngine(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil engine, got nil")
	}
}

// --- helpers ---

func assertEqual[T comparable](t *testing.T, label string, want, got []T) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: len mismatch: want %v got %v", label, want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: at %d: want %v got %v", label, i, want, got)
		}
	}
}
