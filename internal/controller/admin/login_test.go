package admin

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/service/db"
	"github.com/millken/goapp-template/internal/service/session"
	"github.com/millken/inertia"

	// Register the SQLite driver used by the db service in this test.
	_ "github.com/millken/goapp-template/internal/driver"
)

// loginStack wires the real db + session services and the admin controller
// against an in-memory SQLite database (migrations on → users table) with one
// seeded user (alice / "pw"), in the same order serve.go uses.
func loginStack(t *testing.T) (*inertia.Engine, *Admin) {
	t.Helper()
	ctx := context.Background()
	eng := newTestEngine(t)

	// MaxOpenConns=1 so migrations, seed, and login queries all hit the same
	// :memory: connection (a fresh :memory: DB per connection otherwise).
	dbSvc := db.New(&db.Config{
		Driver:       "sqlite3",
		DSN:          ":memory:",
		MaxOpenConns: 1,
		Migrations:   &db.Migrations{},
	})
	if err := dbSvc.Start(ctx); err != nil {
		t.Fatalf("start db: %v", err)
	}
	t.Cleanup(func() { _ = dbSvc.Stop(ctx) })

	sessSvc := session.New(&session.Config{Secret: "test-secret", Store: session.StoreMemory}, dbSvc)
	if err := sessSvc.Start(ctx); err != nil {
		t.Fatalf("start session: %v", err)
	}
	eng.Use(sessSvc.Middleware())

	svc := app.NewServices(slog.Default(), dbSvc.DB(), sessSvc)
	adm := New(svc, &Config{Mount: "/admin"})
	if err := adm.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	adm.Mount(eng)

	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := dbSvc.DB().ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		"alice", hash, time.Now().UnixNano()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return eng, adm
}

func postForm(path string, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestLogin_HappyPath(t *testing.T) {
	eng, adm := loginStack(t)

	// A protected probe route guarded by the same auth middleware admin routes
	// use — this is what verifies auth actually gates access in the new
	// per-route model.
	authed := false
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(c *inertia.Context) { authed = true })

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, postForm("/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}}))

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 after login, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("expected redirect to /admin, got %q", loc)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a session cookie set on login (before the redirect body)")
	}
	signed := cookies[0].Value

	// Follow-up to the protected route with the cookie must reach the handler.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w2, r2)
	if !authed {
		t.Fatal("expected authenticated follow-up request to reach the protected handler")
	}
}

func TestProtectedRoute_RedirectsWhenUnauthenticated(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(c *inertia.Context) {
		t.Error("handler must not run for an unauthenticated request")
	})

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/probe", nil))

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302 for unauthenticated request, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/login" {
		t.Fatalf("expected redirect to /admin/login, got %q", loc)
	}
}

func TestLogin_BadPassword(t *testing.T) {
	eng, _ := loginStack(t)

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, postForm("/admin/login", url.Values{"username": {"alice"}, "password": {"wrong"}}))

	if w.Code == http.StatusFound {
		t.Fatal("bad password must not redirect (no login)")
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("bad password must not set a session cookie")
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	eng, _ := loginStack(t)

	// Log in first to get a valid cookie.
	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, postForm("/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}}))
	signed := w1.Result().Cookies()[0].Value

	// Log out carrying the cookie.
	w2 := httptest.NewRecorder()
	r2 := postForm("/admin/logout", url.Values{})
	r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w2, r2)

	if w2.Code != http.StatusFound {
		t.Fatalf("expected 302 after logout, got %d", w2.Code)
	}
	cleared := false
	for _, ck := range w2.Result().Cookies() {
		if ck.Name == "session" && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("expected the session cookie to be cleared on logout")
	}
}
