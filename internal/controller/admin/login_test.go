package admin

import (
	"context"
	"encoding/json"
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

	// Register the SQLite driver for this test.
	_ "github.com/millken/goapp-template/internal/driver"
)

// loginStack wires db + session + admin against an in-memory SQLite DB
// (migrations on) with one seeded user (alice / "pw"), in serve.go's order.
func loginStack(t *testing.T) (*inertia.Engine, *Admin) {
	t.Helper()
	ctx := context.Background()
	eng := newTestEngine(t)

	// MaxOpenConns=1 so migrations, seed, and queries hit the same :memory:
	// connection (otherwise each gets a fresh DB).
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

	// Assemble the container the way serve.go does: empty, then one field per
	// component after its Start.
	svc := app.NewServices(slog.Default())
	svc.DB = dbSvc.DB()

	sessSvc := session.New(&session.Config{Secret: "test-secret", Store: session.StoreMemory}, svc.DB)
	if err := sessSvc.Start(ctx); err != nil {
		t.Fatalf("start session: %v", err)
	}
	svc.Session = sessSvc
	eng.Use(sessSvc.Middleware())

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
		`INSERT INTO users (username, password_hash, created_at, group_id)
		 VALUES (?, ?, ?, (SELECT id FROM user_groups WHERE name = 'Administrators'))`,
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

	// A probe route guarded by the same auth middleware admin routes use — this
	// verifies auth actually gates access per-route.
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

// loginAndGetCookie signs in as the seeded user and returns the session cookie.
func loginAndGetCookie(t *testing.T, eng *inertia.Engine) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, postForm("/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}}))
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("login did not set a session cookie (status %d)", w.Code)
	}
	return &http.Cookie{Name: cookies[0].Name, Value: cookies[0].Value}
}

// TestLoginForm_RedirectsWhenAlreadyAuthenticated: after signing in, going back
// to the login page — by the back button or by typing the URL — should land on
// the dashboard rather than offering a second sign-in.
func TestLoginForm_RedirectsWhenAlreadyAuthenticated(t *testing.T) {
	eng, _ := loginStack(t)
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 for an authenticated visit to the login page", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("Location = %q, want /admin", loc)
	}
}

func TestLoginForm_RendersWhenNotAuthenticated(t *testing.T) {
	eng, _ := loginStack(t)

	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/login", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an anonymous visitor must get the form", w.Code)
	}
}

// TestRedirects_UnderPJAX: every admin redirect must become a payload when the
// client is doing a PJAX navigation, or the client would follow the 3xx
// transparently and end up showing one page while the address bar names another.
func TestRedirects_UnderPJAX(t *testing.T) {
	cases := []struct {
		name         string
		authenticate bool
		path         string
		wantRedirect string
	}{
		{"guarded route while anonymous", false, "/admin/probe", "/admin/login"},
		{"login page while signed in", true, "/admin/login", "/admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng, adm := loginStack(t)
			eng.GET("/admin/probe", adm.AuthMiddleware(), func(c *inertia.Context) {})

			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.authenticate {
				r.AddCookie(loginAndGetCookie(t, eng))
			}
			r.Header.Set("X-Pjax", "true")

			w := httptest.NewRecorder()
			eng.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: a PJAX redirect is a payload", w.Code)
			}
			var body map[string]any
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v (raw: %q)", err, w.Body.String())
			}
			if got := body["redirect"]; got != tc.wantRedirect {
				t.Errorf(`body["redirect"] = %v, want %q`, got, tc.wantRedirect)
			}
		})
	}
}
