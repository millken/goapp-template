package admin

import (
	"context"
	"encoding/json"
	"errors"
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
	return loginStackWithStore(t, session.StoreMemory)
}

// loginStackWithStore is loginStack with the session store chosen by the caller.
// The two stores are not interchangeable: store_db round-trips values through
// JSON, so anything read back out of a session has JSON's types, not Go's. A
// test that only ever runs on StoreMemory cannot see that.
func loginStackWithStore(t *testing.T, store session.StoreKind) (*inertia.Engine, *Admin) {
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

	sessSvc := session.New(&session.Config{Secret: "test-secret", Store: store}, svc.DB)
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

// csrfFor drives a GET through the stack and returns the token the page was
// given, plus the cookie it belongs to — freshly minted from the response if
// cookie was nil, since an anonymous GET is exactly how an anonymous visitor
// gets a session at all. Tests that post go through here, the same way a
// browser gets a token by loading the form first.
func csrfFor(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if cookie == nil {
		cs := w.Result().Cookies()
		if len(cs) == 0 {
			t.Fatalf("GET %s did not set a session cookie (status %d)", path, w.Code)
		}
		cookie = &http.Cookie{Name: cs[0].Name, Value: cs[0].Value}
	}

	body := w.Body.String()
	const marker = `name="_csrf" value="`
	if i := strings.Index(body, marker); i >= 0 {
		rest := body[i+len(marker):]
		return rest[:strings.Index(rest, `"`)], cookie
	}
	// Fall back to the page data, which carries the prop even when the
	// markup is not rendered in this mode.
	const prop = `\"csrfToken\":\"`
	if j := strings.Index(body, prop); j >= 0 {
		rest := body[j+len(prop):]
		return rest[:strings.Index(rest, `\"`)], cookie
	}
	t.Fatalf("no csrf token on %s (status %d)", path, w.Code)
	return "", nil
}

// postForm builds a POST carrying a valid token: an anonymous one (cookie
// nil) fetched from the public login page, an authenticated one fetched from
// the admin mount — mirroring how post (user_crud_test.go) does it for a
// caller that already has a cookie. The token and the cookie it validates
// against always come from the same fetch, because a token minted under one
// session cannot authenticate a request carrying another.
//
// It returns the cookie alongside the request rather than leaving callers to
// recover it from the POST's own response: a session that already has a
// token (which fetching one just gave it) does not re-Save on a request that
// changes nothing else, so the POST response carries no Set-Cookie of its
// own to fish out.
func postForm(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string, form url.Values) (*http.Request, *http.Cookie) {
	t.Helper()
	tokenPath := "/admin/login"
	if cookie != nil {
		tokenPath = "/admin"
	}
	token, ck := csrfFor(t, eng, cookie, tokenPath)

	values := url.Values{}
	for k, v := range form {
		values[k] = append([]string(nil), v...)
	}
	values.Set(session.CSRFFormField, token)

	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(ck)
	return r, ck
}

func TestLogin_HappyPath(t *testing.T) {
	eng, adm := loginStack(t)

	// A probe route guarded by the same auth middleware admin routes use — this
	// verifies auth actually gates access per-route.
	authed := false
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(c *inertia.Context) { authed = true })

	r, _ := postForm(t, eng, nil, "/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

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
	eng, adm := loginStack(t)

	req, cookie := postForm(t, eng, nil, "/admin/login", url.Values{"username": {"alice"}, "password": {"wrong"}})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)

	if w.Code == http.StatusFound {
		t.Fatal("bad password must not redirect (no login)")
	}
	// A session is now expected before the POST even runs — postForm had to
	// fetch a token from the login page first, and minting one is what gives
	// an anonymous visitor a session at all. Because that session already
	// carried the token going in, the failure re-render does not re-Save and
	// so this response sets no additional cookie of its own; the one that
	// matters is the one the request already carried. What this test was
	// really protecting is that a failed attempt leaves you unauthenticated,
	// so it asserts that directly, against that cookie.
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(c *inertia.Context) {
		t.Error("a failed login produced a session that reaches guarded routes")
	})
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	eng.ServeHTTP(w2, r)
	if w2.Code != http.StatusFound {
		t.Errorf("status = %d, want a redirect to login — the session must not be authenticated", w2.Code)
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	eng, _ := loginStack(t)

	// Log in first to get a valid cookie.
	loginReq, _ := postForm(t, eng, nil, "/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}})
	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, loginReq)
	signed := w1.Result().Cookies()[0].Value
	cookie := &http.Cookie{Name: "session", Value: signed}

	// Log out carrying the cookie — postForm fetches a fresh token bound to
	// it from the admin mount, the same way a browser on an admin page would.
	logoutReq, _ := postForm(t, eng, cookie, "/admin/logout", url.Values{})
	w2 := httptest.NewRecorder()
	eng.ServeHTTP(w2, logoutReq)

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
	r, _ := postForm(t, eng, nil, "/admin/login", url.Values{"username": {"alice"}, "password": {"pw"}})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
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

// The order matters: status is checked only after the password verifies, so a
// disabled account is not distinguishable from a wrong password by timing.
// Whoever sees the disabled error already proved they hold the credential, so
// saying so plainly leaks nothing — and beats sending the real owner hunting
// for a password problem that does not exist.
func TestAuthenticate_DisabledAccount(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	// Correct password, disabled account: a distinct error, not "invalid".
	_, err := authenticate(ctx, adm.DB, "users", "alice", "pw")
	if err == nil {
		t.Fatal("a disabled account must not authenticate")
	}
	if errors.Is(err, errInvalidCredentials) {
		t.Error("a disabled account should report being disabled, not invalid credentials")
	}
	if !errors.Is(err, errAccountDisabled) {
		t.Errorf("want errAccountDisabled, got %v", err)
	}

	// Wrong password on a disabled account stays "invalid credentials": the
	// caller has not proved anything, so nothing may be revealed.
	if _, err := authenticate(ctx, adm.DB, "users", "alice", "wrong"); !errors.Is(err, errInvalidCredentials) {
		t.Errorf("wrong password on a disabled account: want errInvalidCredentials, got %v", err)
	}
}
