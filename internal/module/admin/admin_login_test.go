package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/module/db"
	"github.com/millken/goapp-template/internal/module/session"
	"github.com/millken/inertia"

	// Register the SQLite driver used by the db module in this test.
	_ "github.com/millken/goapp-template/internal/driver"
)

// loginStack wires the real db + session + admin modules against an in-memory
// SQLite database (migrations on → users table) with one seeded user
// (alice / "pw"), booted in the correct order.
func loginStack(t *testing.T) (*inertia.Engine, *Module) {
	t.Helper()
	ctx := context.Background()
	eng := newTestEngine(t)
	a, err := app.New(eng)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	// MaxOpenConns=1 so migrations, seed, and login queries all hit the same
	// :memory: connection (a fresh :memory: DB per connection otherwise).
	dbMod := db.New(&db.Config{
		Driver:       "sqlite3",
		DSN:          ":memory:",
		MaxOpenConns: 1,
		Migrations:   &db.Migrations{},
	})
	sessMod := session.New(&session.Config{Secret: "test-secret", Store: session.StoreMemory}, dbMod)
	adminMod := New(&Config{Mount: "/admin"}, sessMod, dbMod)

	for _, b := range []interface {
		Boot(context.Context) error
	}{dbMod, sessMod, adminMod} {
		if err := b.Boot(ctx); err != nil {
			t.Fatalf("Boot: %v", err)
		}
	}
	if err := a.Use(dbMod, sessMod, adminMod); err != nil {
		t.Fatalf("Use: %v", err)
	}

	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := dbMod.DB().ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		"alice", hash, time.Now().UnixNano()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return eng, adminMod
}

func postForm(path string, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestLogin_HappyPath(t *testing.T) {
	eng, _ := loginStack(t)

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

	// Follow-up request to a protected route with the cookie must be allowed.
	authed := false
	eng.GET("/admin/posts", func(c *inertia.Context) { authed = true })
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/admin/posts", nil)
	r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
	eng.ServeHTTP(w2, r2)
	if !authed {
		t.Fatal("expected authenticated follow-up request to reach the protected handler")
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
