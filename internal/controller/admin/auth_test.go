package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/millken/goapp-template/internal/service/session"
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

	if w.Code == http.StatusForbidden {
		t.Error("a disabled account answered 403 — that is the no-group answer")
	}
	if w.Code == http.StatusInternalServerError {
		t.Error("a disabled account answered 500 — that is the storage-failure answer")
	}
}
