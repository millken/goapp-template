package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

func newTestEngine(t *testing.T) *inertia.Engine {
	t.Helper()
	eng, err := inertia.New(inertia.WithMode(inertia.ModeProduction))
	if err != nil {
		t.Fatalf("inertia.New: %v", err)
	}
	return eng
}

// TestBoot_NilConfig verifies the enable-consistency rule (§4.4).
func TestBoot_NilConfig(t *testing.T) {
	m := New(nil, nil)
	err := m.Boot(context.Background())
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config section missing") {
		t.Fatalf("expected 'config section missing', got %q", err.Error())
	}
}

func TestBoot_MissingSecret(t *testing.T) {
	m := New(&Config{Store: StoreMemory}, nil)
	err := m.Boot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "secret is required") {
		t.Fatalf("expected secret-required error, got %v", err)
	}
}

func TestBoot_UnknownStore(t *testing.T) {
	m := New(&Config{Secret: "k", Store: "redis"}, nil)
	err := m.Boot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown store") {
		t.Fatalf("expected unknown-store error, got %v", err)
	}
}

func TestBoot_DBStoreWithoutProvider(t *testing.T) {
	m := New(&Config{Secret: "k", Store: StoreDB}, nil)
	err := m.Boot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires a db.Provider") {
		t.Fatalf("expected db.Provider-required error, got %v", err)
	}
}

func TestBoot_MemoryDefault(t *testing.T) {
	// Empty store defaults to memory.
	m := New(&Config{Secret: "k"}, nil)
	if err := m.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if m.store == nil {
		t.Fatal("expected memory store to be set")
	}
}

// TestModule_SatisfiesInterfaces asserts the concrete *Module implements the
// app lifecycle hooks and the Provider contract.
func TestModule_SatisfiesInterfaces(t *testing.T) {
	var (
		_ app.Module     = (*Module)(nil)
		_ app.Booter     = (*Module)(nil)
		_ app.Shutdowner = (*Module)(nil)
		_ Provider       = (*Module)(nil)
	)
}

// TestMiddleware_AutoWritesCookieOnSave is the regression test for the HIGH
// bug where the session cookie never reached the response. The handler calls
// Save and THEN writes a body (the normal case: render a page / return JSON) —
// inertia's writer is write-through, so the cookie must be emitted by Save
// before the body flushes, not after the handler returns. A follow-up request
// with that cookie must load the saved values.
func TestMiddleware_AutoWritesCookieOnSave(t *testing.T) {
	eng := newTestEngine(t)
	a, err := app.New(eng)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	mod := New(&Config{Secret: "test-secret", Store: StoreMemory}, nil)
	if err := mod.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := a.Use(mod); err != nil {
		t.Fatalf("Use: %v", err)
	}

	eng.GET("/login", func(c *inertia.Context) {
		s := mod.Session(c)
		s.Set("user", "alice")
		if _, err := s.Save(c.Request.Context()); err != nil {
			t.Errorf("Save: %v", err)
			return
		}
		// Write a body after Save, as a real handler would. This flushes the
		// header block; the Set-Cookie must already be on it.
		_, _ = c.Writer.Write([]byte("<html>ok</html>"))
	})

	// First request: no cookie → fresh session; Save writes the cookie.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/login", nil)
	eng.ServeHTTP(w1, r1)

	cookies := w1.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected middleware to set 1 cookie, got %d", len(cookies))
	}
	signed := cookies[0].Value
	if signed == "" {
		t.Fatal("expected non-empty cookie value")
	}

	// Second request: carry the cookie → middleware loads the saved session.
	var loadedUser any
	var loadedOk bool
	eng.GET("/whoami", func(c *inertia.Context) {
		s := mod.Session(c)
		loadedUser, loadedOk = s.Get("user")
	})
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w2, r2)

	if !loadedOk || loadedUser != "alice" {
		t.Fatalf("expected to load user=alice, got ok=%v user=%v", loadedOk, loadedUser)
	}
}

// TestMiddleware_ClearsCookieOnDestroy verifies Destroy causes the middleware
// to clear the cookie, and that a follow-up request with the stale cookie does
// not resurrect the destroyed session.
func TestMiddleware_ClearsCookieOnDestroy(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	mod := New(&Config{Secret: "test-secret", Store: StoreMemory}, nil)
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	// Seed a session via /login (Save → cookie).
	var signed string
	eng.GET("/login", func(c *inertia.Context) {
		s := mod.Session(c)
		s.Set("user", "alice")
		_, _ = s.Save(c.Request.Context())
	})
	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/login", nil))
	signed = w1.Result().Cookies()[0].Value

	// Destroy it: the cookie must be cleared even though the handler then writes
	// a body (write-through writer — clear must happen before the flush).
	eng.GET("/logout", func(c *inertia.Context) {
		_ = mod.Session(c).Destroy(c.Request.Context())
		_, _ = c.Writer.Write([]byte("bye"))
	})
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/logout", nil)
	r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w2, r2)

	clearCookies := w2.Result().Cookies()
	foundCleared := false
	for _, ck := range clearCookies {
		if ck.Name == "session" && ck.MaxAge < 0 {
			foundCleared = true
		}
	}
	if !foundCleared {
		t.Fatal("expected middleware to clear cookie on Destroy")
	}

	// Follow-up with the stale cookie: session should be gone from the store.
	var ok bool
	eng.GET("/check", func(c *inertia.Context) {
		_, ok = mod.Session(c).Get("user")
	})
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodGet, "/check", nil)
	r3.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w3, r3)
	if ok {
		t.Fatal("expected destroyed session to be gone")
	}
}

// TestMiddleware_NoCookieWhenSessionUntouched verifies the middleware does not
// set a cookie when the handler never calls Save/Destroy (read-only access).
func TestMiddleware_NoCookieWhenSessionUntouched(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	mod := New(&Config{Secret: "k", Store: StoreMemory}, nil)
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	eng.GET("/", func(c *inertia.Context) {
		_ = mod.Session(c) // touch but don't mutate
	})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("expected no cookie for an untouched session")
	}
}

// TestMiddleware_TamperedCookieRejected ensures a tampered cookie yields a fresh
// empty session rather than loading attacker-controlled data.
func TestMiddleware_TamperedCookieRejected(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	mod := New(&Config{Secret: "k", Store: StoreMemory}, nil)
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	var ok bool
	eng.GET("/", func(c *inertia.Context) {
		_, ok = mod.Session(c).Get("anything")
	})

	// A cookie that verifies as tampered.
	bad := signCookie("k", "id") + "x"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: bad})
	eng.ServeHTTP(w, r)

	if ok {
		t.Fatal("expected fresh session (no data) for tampered cookie")
	}
}

// TestShutdown_Noop verifies Shutdown is safe and returns nil.
func TestShutdown_Noop(t *testing.T) {
	mod := New(&Config{Secret: "k", Store: StoreMemory}, nil)
	_ = mod.Boot(context.Background())
	if err := mod.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
