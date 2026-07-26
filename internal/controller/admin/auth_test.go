package admin

import (
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
