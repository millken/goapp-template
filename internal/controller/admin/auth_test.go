package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/service/session"
	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// config.example.yaml names `store: db` as the production setting, and store_db
// round-trips session values through JSON — so the user id LoginSubmit wrote as
// an int64 comes back as a float64 on the next request. Every other admin test
// runs on StoreMemory, which returns values as written, so none of them can see
// this: the whole admin area answers 500 after a normal login.
func TestResolve_WorksWithTheDatabaseSessionStore(t *testing.T) {
	eng, adm := loginStackWithStore(t, session.StoreDB)

	ran := false
	eng.GET("/admin/probe", adm.guard("post.access"), func(ic *inertia.Context) { ran = true })
	putInGroup(t, adm, "TestGroup", false, `["post.access"]`)
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — the group lookup did not get the user id "+
			"back out of a db-backed session", w.Code)
	}
	if !ran {
		t.Error("handler did not run")
	}
}

// The shell's topbar shows who is signed in and where they are; both come from
// resolve, in the same single query it already ran.
func TestResolve_InjectsUsernameAndCurrentPath(t *testing.T) {
	eng, adm := loginStack(t)

	var gotUser any
	var gotPath any
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		gotUser, _ = ic.Get("adminUser")
		gotPath, _ = ic.Get("currentPath")
	})
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	u, ok := gotUser.(map[string]any)
	if !ok {
		t.Fatalf("adminUser = %#v, want a map", gotUser)
	}
	if u["username"] != "alice" {
		t.Errorf("adminUser.username = %v, want alice", u["username"])
	}
	if u["id"] == nil {
		t.Error("adminUser.id missing")
	}
	if gotPath != "/admin/probe" {
		t.Errorf("currentPath = %v, want /admin/probe", gotPath)
	}
}

// AdminShell shows the signed-in user's avatar beside their username, and both
// come from adminUser — which comes from resolve's one query, the same one that
// already loads username and status. A user with no avatar (the common case,
// and every other stack's alice) must not be confused with one that has it, so
// this sets one explicitly rather than trusting the seed data's default.
func TestResolve_InjectsAdminUserAvatar(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET avatar = 'photos/alice.png' WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	var gotUser any
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		gotUser, _ = ic.Get("adminUser")
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	u, ok := gotUser.(map[string]any)
	if !ok {
		t.Fatalf("adminUser = %#v, want a map", gotUser)
	}
	if u["avatar"] != "photos/alice.png" {
		t.Errorf("adminUser.avatar = %v, want photos/alice.png", u["avatar"])
	}
}

// The counterpart to the case above: a user with no avatar must not have one
// manufactured for them. Without this, a stub that hardcoded a filename would
// pass the test above and go undetected.
func TestResolve_AdminUserAvatarIsEmptyWhenUnset(t *testing.T) {
	eng, adm, cookie := adminStack(t)

	var gotUser any
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		gotUser, _ = ic.Get("adminUser")
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	u, ok := gotUser.(map[string]any)
	if !ok {
		t.Fatalf("adminUser = %#v, want a map", gotUser)
	}
	if u["avatar"] != "" {
		t.Errorf("adminUser.avatar = %v, want empty for a user with none set", u["avatar"])
	}
}

// urlPrefix is set once in resolve, next to adminMount, rather than by every
// handler that used to set its own copy. A custom prefix (not the default
// "/uploads") proves the value on the wire actually came from the configured
// service rather than from a hardcoded fallback that happens to match the
// default.
func TestResolve_DeliversURLPrefix(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	stor := storage.New(&storage.Config{Root: t.TempDir(), URLPrefix: "/media"})
	if err := stor.Start(context.Background()); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(context.Background()) })
	adm.Storage = stor

	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	// The page body embeds the Inertia props as JSON spliced into a quoted JS
	// string literal, so every `"` is written out as `\"` — see
	// TestResolve_DeliversCanBrowseFiles for the same reasoning.
	if !strings.Contains(w.Body.String(), `urlPrefix\":\"/media\"`) {
		t.Errorf("urlPrefix did not reach the page as a prop with the configured value; body: %s", w.Body.String())
	}
}

// userID coerces because the two session stores disagree about number types, but
// coercion must not turn a value this code never wrote into a valid id.
func TestUserID(t *testing.T) {
	for _, c := range []struct {
		name string
		v    any
		want int64
		ok   bool
	}{
		{"int64 as store_memory returns it", int64(7), 7, true},
		{"float64 as store_db returns it", float64(7), 7, true},
		{"int", 7, 7, true},
		{"json.Number", json.Number("7"), 7, true},
		{"non-integral float is not an id", 7.5, 0, false},
		{"string", "7", 0, false},
		{"nil", nil, 0, false},
		{"map", map[string]any{}, 0, false},
	} {
		got, ok := userID(c.v)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: userID(%#v) = (%d, %v), want (%d, %v)", c.name, c.v, got, ok, c.want, c.ok)
		}
	}
}

// A disabled user holding a live session gets bounced to login with an
// explanation, not a bare 403 — and the session is deliberately NOT destroyed,
// because a flash is session state and Destroy also clears the cookie it would
// travel in.
func TestResolve_DisabledUserIsBouncedToLoginWithAFlash(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		t.Error("handler must not run for a disabled user")
	})
	cookie := loginAndGetCookie(t, eng)

	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther && w.Code != http.StatusFound {
		t.Errorf("status = %d, want a redirect to login", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

// Every attempt should say why it failed. Re-staging the flash on each request
// is intended: the alternative is an "already told them" marker in the session,
// which is state to no purpose. Pinned so the repetition is not mistaken for a
// defect later.
func TestResolve_DisabledUserIsBouncedOnEveryRequest(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {})
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
		r.AddCookie(cookie)
		eng.ServeHTTP(w, r)
		if loc := w.Header().Get("Location"); loc != "/admin/login" {
			t.Errorf("request %d: Location = %q, want /admin/login", i+1, loc)
		}
	}
}

// The three outcomes must stay apart: conflating them would make an outage or a
// disabled account look like a permissions decision.
func TestResolve_DisabledIsNeitherForbiddenNorInternalError(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {})
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	// Asserting the redirect too: "neither 403 nor 500" is satisfied by a 200,
	// so on its own this case would pass against an implementation that ignored
	// status entirely.
	if w.Code != http.StatusSeeOther && w.Code != http.StatusFound {
		t.Errorf("status = %d, want a redirect", w.Code)
	}
	if w.Code == http.StatusForbidden {
		t.Error("a disabled account answered 403 — that is the no-group answer")
	}
	if w.Code == http.StatusInternalServerError {
		t.Error("a disabled account answered 500 — that is the storage-failure answer")
	}
}

// The flash is the entire point of not destroying the session on this path, so
// staging it is not enough — it has to arrive. This follows the bounce to the
// login page and reads the message off the rendered response.
func TestResolve_DisabledUserSeesTheReasonOnTheLoginPage(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {})
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	// The bounce, which stages the flash.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	// Carry whatever cookie that response set — the session was re-saved, so its
	// id may have changed — and follow the redirect.
	next := cookie
	if cs := w.Result().Cookies(); len(cs) > 0 {
		next = &http.Cookie{Name: cs[0].Name, Value: cs[0].Value}
	}
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	r2.AddCookie(next)
	eng.ServeHTTP(w2, r2)

	if !strings.Contains(w2.Body.String(), "该账号已被禁用") {
		t.Errorf("the login page did not carry the reason; body: %s", w2.Body.String())
	}
}

// Deleting a user does not purge their session, and findCaller cannot tell a
// deleted user from one with no group — its join drops both. Without an escape
// hatch that covers them, the deleted user gets 403 on every admin page, 403 on
// logout, and a login page that redirects them back to the 403: clearing cookies
// by hand would be the only way out.
func TestLoginForm_RendersForADeletedUsersSession(t *testing.T) {
	eng, adm := loginStack(t)
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`DELETE FROM users WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code == http.StatusFound {
		t.Fatalf("redirected to %q — a deleted user must be able to reach the form",
			w.Header().Get("Location"))
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// Same trap by a different route: a group deleted out from under a user, or a
// dangling group_id, is indistinguishable from deletion to findCaller.
func TestLoginForm_RendersForAGrouplessSession(t *testing.T) {
	eng, adm := loginStack(t)
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET group_id = NULL WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a group-less user must reach the form", w.Code)
	}
}
