package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// installed builds a Started service and installs its middleware on eng, the way
// serve.go wires it (eng.Use(svc.Middleware())).
func installed(t *testing.T, eng *inertia.Engine, cfg *Config) *Service {
	t.Helper()
	svc := New(cfg, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eng.Use(svc.Middleware())
	return svc
}

// TestStart_NilConfig verifies the enable-consistency rule.
func TestStart_NilConfig(t *testing.T) {
	s := New(nil, nil)
	err := s.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "config section missing") {
		t.Fatalf("expected 'config section missing', got %q", err.Error())
	}
}

func TestStart_MissingSecret(t *testing.T) {
	s := New(&Config{Store: StoreMemory}, nil)
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "secret is required") {
		t.Fatalf("expected secret-required error, got %v", err)
	}
}

func TestStart_UnknownStore(t *testing.T) {
	s := New(&Config{Secret: "k", Store: "redis"}, nil)
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown store") {
		t.Fatalf("expected unknown-store error, got %v", err)
	}
}

func TestStart_DBStoreWithoutProvider(t *testing.T) {
	s := New(&Config{Secret: "k", Store: StoreDB}, nil)
	err := s.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires a db.Provider") {
		t.Fatalf("expected db.Provider-required error, got %v", err)
	}
}

func TestStart_MemoryDefault(t *testing.T) {
	// Empty store defaults to memory.
	s := New(&Config{Secret: "k"}, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.store == nil {
		t.Fatal("expected memory store to be set")
	}
}

// TestService_SatisfiesProvider asserts *Service implements Provider. The
// app.Lifecycle assertion lives with the composition root (commands), not here:
// app imports session, so an internal test importing app would be a cycle.
func TestService_SatisfiesProvider(t *testing.T) {
	var _ Provider = (*Service)(nil)
}

// TestMiddleware_AutoWritesCookieOnSave is the regression test for the HIGH bug
// where the session cookie never reached the response. The handler calls Save
// and THEN writes a body (the normal case: render a page / return JSON) —
// inertia's writer is write-through, so the cookie must be emitted by Save
// before the body flushes, not after the handler returns. A follow-up request
// with that cookie must load the saved values.
func TestMiddleware_AutoWritesCookieOnSave(t *testing.T) {
	eng := newTestEngine(t)
	svc := installed(t, eng, &Config{Secret: "test-secret", Store: StoreMemory})

	eng.GET("/login", func(c *inertia.Context) {
		s := svc.Session(c)
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
		s := svc.Session(c)
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

// TestMiddleware_ClearsCookieOnDestroy verifies Destroy causes the middleware to
// clear the cookie, and that a follow-up request with the stale cookie does not
// resurrect the destroyed session.
func TestMiddleware_ClearsCookieOnDestroy(t *testing.T) {
	eng := newTestEngine(t)
	svc := installed(t, eng, &Config{Secret: "test-secret", Store: StoreMemory})

	// Seed a session via /login (Save → cookie).
	var signed string
	eng.GET("/login", func(c *inertia.Context) {
		s := svc.Session(c)
		s.Set("user", "alice")
		_, _ = s.Save(c.Request.Context())
	})
	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/login", nil))
	signed = w1.Result().Cookies()[0].Value

	// Destroy it: the cookie must be cleared even though the handler then writes a
	// body (write-through writer — clear must happen before the flush).
	eng.GET("/logout", func(c *inertia.Context) {
		_ = svc.Session(c).Destroy(c.Request.Context())
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
		_, ok = svc.Session(c).Get("user")
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
	svc := installed(t, eng, &Config{Secret: "k", Store: StoreMemory})

	eng.GET("/", func(c *inertia.Context) {
		_ = svc.Session(c) // touch but don't mutate
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
	svc := installed(t, eng, &Config{Secret: "k", Store: StoreMemory})

	var ok bool
	eng.GET("/", func(c *inertia.Context) {
		_, ok = svc.Session(c).Get("anything")
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

// TestStop_Noop verifies Stop is safe and returns nil.
func TestStop_Noop(t *testing.T) {
	svc := New(&Config{Secret: "k", Store: StoreMemory}, nil)
	_ = svc.Start(context.Background())
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
