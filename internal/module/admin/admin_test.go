package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/module/session"
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

// stubSession is a minimal Session for auth-middleware tests, controlling the
// auth key value to simulate logged-in / logged-out states.
type stubSession struct {
	values map[string]any
}

func (s *stubSession) ID() string                           { return "" }
func (s *stubSession) Get(k string) (any, bool)             { v, ok := s.values[k]; return v, ok }
func (s *stubSession) Set(string, any)                      {}
func (s *stubSession) Delete(string)                        {}
func (s *stubSession) Save(context.Context) (string, error) { return "", nil }
func (s *stubSession) Destroy(context.Context) error        { return nil }

type stubSessionProvider struct{ sess session.Session }

func (p stubSessionProvider) Session(_ *inertia.Context) session.Session { return p.sess }

// stubDBProvider satisfies DBProvider for tests that never touch the database
// (auth middleware / Boot validation). DB() returns nil; it is not called.
type stubDBProvider struct{}

func (stubDBProvider) DB() *sqldb.DB { return nil }

func TestBoot_NilConfig(t *testing.T) {
	m := New(nil, stubSessionProvider{}, stubDBProvider{})
	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestRegister_NilConfigNoPanic guards the regression where a missing [admin]
// section (nil cfg) segfaulted in Register (which runs at a.Use time, before
// Boot) via the config accessors. Register must succeed with defaults; the
// missing-section error is surfaced later by Boot.
func TestRegister_NilConfigNoPanic(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	m := New(nil, stubSessionProvider{sess: &stubSession{values: map[string]any{}}}, stubDBProvider{})

	if err := a.Use(m); err != nil { // triggers Register + authMiddleware(); must not panic
		t.Fatalf("Use with nil config panicked or errored: %v", err)
	}
	// A request still routes (middleware built with default mount "/admin").
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	// And Boot reports the missing section cleanly.
	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("expected Boot to report missing [admin] config section")
	}
}

func TestBoot_NilSessionProvider(t *testing.T) {
	m := New(&Config{}, nil, stubDBProvider{})
	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("expected error for nil session provider")
	}
}

func TestBoot_NilDBProvider(t *testing.T) {
	m := New(&Config{}, stubSessionProvider{}, nil)
	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("expected error for nil db provider")
	}
}

func TestBoot_BadUsersTable(t *testing.T) {
	m := New(&Config{UsersTable: "bad-name!"}, stubSessionProvider{}, stubDBProvider{})
	if err := m.Boot(context.Background()); err == nil {
		t.Fatal("expected error for illegal users table name")
	}
}

func TestAuthMiddleware_BlocksUnauthenticated(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	prov := stubSessionProvider{sess: &stubSession{values: map[string]any{}}}
	mod := New(&Config{Mount: "/admin"}, prov, stubDBProvider{})
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	called := false
	eng.GET("/admin/posts", func(c *inertia.Context) { called = true })

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/posts", nil))

	if called {
		t.Fatal("handler should NOT run for unauthenticated request")
	}
	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/login" {
		t.Fatalf("expected redirect to /admin/login, got %q", loc)
	}
}

func TestAuthMiddleware_AllowsAuthenticated(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	prov := stubSessionProvider{sess: &stubSession{values: map[string]any{"admin_user_id": "u-1"}}}
	mod := New(&Config{Mount: "/admin"}, prov, stubDBProvider{})
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	called := false
	eng.GET("/admin/posts", func(c *inertia.Context) { called = true })

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/posts", nil))

	if !called {
		t.Fatal("handler should run for authenticated request")
	}
}

func TestAuthMiddleware_LoginPathPublic(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	prov := stubSessionProvider{sess: &stubSession{values: map[string]any{}}}
	mod := New(&Config{Mount: "/admin"}, prov, stubDBProvider{})
	_ = mod.Boot(context.Background())
	_ = a.Use(mod) // admin registers its own GET /admin/login (loginForm)

	// Unauthenticated GET to the login path must pass through the middleware
	// (reach admin's own loginForm) rather than being redirected to login.
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/login", nil))

	if w.Code == http.StatusFound {
		t.Fatalf("login path should be public, but middleware redirected (302)")
	}
}

func TestAuthMiddleware_NonAdminPassesThrough(t *testing.T) {
	eng := newTestEngine(t)
	a, _ := app.New(eng)
	prov := stubSessionProvider{sess: &stubSession{values: map[string]any{}}}
	mod := New(&Config{Mount: "/admin"}, prov, stubDBProvider{})
	_ = mod.Boot(context.Background())
	_ = a.Use(mod)

	called := false
	eng.GET("/public", func(c *inertia.Context) { called = true })

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/public", nil))

	if !called {
		t.Fatal("non-admin route should pass through")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestIsUnder(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"/admin", "/admin", true},
		{"/admin/posts", "/admin", true},
		{"/adminfoo", "/admin", false},
		{"/other", "/admin", false},
	}
	for _, c := range cases {
		if got := isUnder(c.path, c.prefix); got != c.want {
			t.Errorf("isUnder(%q,%q)=%v, want %v", c.path, c.prefix, got, c.want)
		}
	}
}
