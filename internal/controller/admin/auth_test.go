package admin

import (
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
