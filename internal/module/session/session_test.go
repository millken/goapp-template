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

// TestMiddleware_LoadsAndCreates exercises the full request flow: a request
// with no cookie gets a fresh session; after Save the response carries a signed
// cookie; a follow-up request with that cookie loads the saved values.
func TestMiddleware_LoadsAndCreates(t *testing.T) {
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
		id, err := s.Save(c.Request.Context())
		if err != nil {
			t.Errorf("Save: %v", err)
			return
		}
		mod.setCookie(c.Writer, id)
	})

	// First request: no cookie → fresh session, saved → Set-Cookie in response.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/login", nil)
	eng.ServeHTTP(w1, r1)

	cookies := w1.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 Set-Cookie, got %d", len(cookies))
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
