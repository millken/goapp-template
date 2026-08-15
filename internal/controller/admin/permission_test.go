package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/millken/inertia"
)

func TestPermKey(t *testing.T) {
	for _, c := range []struct{ method, want string }{
		{http.MethodGet, "post.access"},
		{http.MethodHead, "post.access"},
		{http.MethodPost, "post.modify"},
		{http.MethodPut, "post.modify"},
		{http.MethodPatch, "post.modify"},
		{http.MethodDelete, "post.modify"},
	} {
		if got := permKey("post", c.method); got != c.want {
			t.Errorf("permKey(post, %s) = %q, want %q", c.method, got, c.want)
		}
	}
}

// The implication is one-way. A test that only checked modify→access would pass
// for an implementation that allowed everything.
func TestPermSetAllows(t *testing.T) {
	modifyOnly := permSet{"post.modify": true}
	if !modifyOnly.Allows("post.modify") {
		t.Error("modify should allow modify")
	}
	if !modifyOnly.Allows("post.access") {
		t.Error("modify should imply access")
	}

	accessOnly := permSet{"post.access": true}
	if !accessOnly.Allows("post.access") {
		t.Error("access should allow access")
	}
	if accessOnly.Allows("post.modify") {
		t.Error("access must NOT imply modify — the implication is one-way")
	}

	empty := permSet{}
	if empty.Allows("post.access") || empty.Allows("post.modify") {
		t.Error("an empty set allows nothing")
	}

	// A different resource's key must not leak across.
	if modifyOnly.Allows("user.access") {
		t.Error("post.modify must not allow user.access")
	}
}

func TestPermissionsCatalogue(t *testing.T) {
	a := New(nil, nil)
	a.recordPermission("post.access", http.MethodGet, "/admin/post")
	a.recordPermission("post.access", http.MethodGet, "/admin/post/:id/edit")
	a.recordPermission("post.modify", http.MethodPost, "/admin/post")
	a.recordPermission("user.access", http.MethodGet, "/admin/user")
	// The same route twice must not duplicate.
	a.recordPermission("post.access", http.MethodGet, "/admin/post")

	got := a.Permissions()
	if len(got) != 3 {
		t.Fatalf("want 3 keys, got %d: %+v", len(got), got)
	}
	// Sorted by key, so a UI built on this is stable.
	want := []string{"post.access", "post.modify", "user.access"}
	for i, k := range want {
		if got[i].Key != k {
			t.Errorf("key %d = %q, want %q", i, got[i].Key, k)
		}
	}
	if n := len(got[0].Routes); n != 2 {
		t.Errorf("post.access should list 2 routes, got %d: %v", n, got[0].Routes)
	}
	if got[0].Routes[0] != "GET /admin/post" {
		t.Errorf("route format = %q, want %q", got[0].Routes[0], "GET /admin/post")
	}
}

func TestRegistrar_RegistersRoutesAndRecordsKeys(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	noop := func(c *inertia.Context) {}

	r := a.Resource(eng, "post")
	r.GET("/admin/post", noop)
	r.GET("/admin/post/:id/edit", noop)
	r.POST("/admin/post", noop)
	r.POST("/admin/post/:id/delete", noop)
	r.Menu("Content", "Post", "/admin/post")

	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}

	got := a.Permissions()
	if len(got) != 2 {
		t.Fatalf("want post.access and post.modify, got %+v", got)
	}
	if got[0].Key != "post.access" || len(got[0].Routes) != 2 {
		t.Errorf("post.access = %+v", got[0])
	}
	if got[1].Key != "post.modify" || len(got[1].Routes) != 2 {
		t.Errorf("post.modify = %+v", got[1])
	}

	// Menu goes through addResourceMenuItem, so it is gated by post.access.
	if len(a.menu) != 1 || a.menu[0].resource != "post" || a.menu[0].item.Section != "Content" {
		t.Errorf("Menu should record the resource and section, got %+v", a.menu)
	}
}

// A dot in the name would make permKey produce keys that alias confusingly, and
// this is the one place a name enters the system.
func TestResource_RejectsIllegalNames(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	for _, bad := range []string{"post.access", "Post", "", "my post", "post/sub", ".post"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Resource(%q) should panic", bad)
				}
			}()
			a.Resource(eng, bad)
		}()
	}
	// And a legal one must not panic.
	a.Resource(eng, "blog-post")
}

func TestRegistrar_HandleCoversOtherMethods(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	a.Resource(eng, "post").Handle(http.MethodDelete, "/admin/post/:id", func(c *inertia.Context) {})

	got := a.Permissions()
	if len(got) != 1 || got[0].Key != "post.modify" {
		t.Errorf("DELETE should record post.modify, got %+v", got)
	}
}

// putInGroup moves the harness user into a fresh group with the given flag and
// permission keys, and returns nothing: the caller only cares that the next
// request is evaluated against it.
func putInGroup(t *testing.T, adm *Admin, name string, superuser bool, keysJSON string) {
	t.Helper()
	ctx := context.Background()
	su := 0
	if superuser {
		su = 1
	}
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO admin_groups (name, superuser, permissions, created_at) VALUES (?, ?, ?, 0)`,
		name, su, keysJSON); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE admins SET group_id = (SELECT id FROM admin_groups WHERE name = ?) WHERE username = 'alice'`,
		name); err != nil {
		t.Fatalf("move alice into %s: %v", name, err)
	}
}

func TestGuard_EndToEnd(t *testing.T) {
	for _, c := range []struct {
		name      string
		superuser bool
		keysJSON  string
		checkKey  string
		wantCode  int
		wantRun   bool
	}{
		{"superuser passes", true, `[]`, "post.modify", http.StatusOK, true},
		{"key present passes", false, `["post.modify"]`, "post.modify", http.StatusOK, true},
		{"modify implies access", false, `["post.modify"]`, "post.access", http.StatusOK, true},
		{"key absent is forbidden", false, `["post.modify"]`, "billing.modify", http.StatusForbidden, false},
		{"access does not imply modify", false, `["user.access"]`, "user.modify", http.StatusForbidden, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm := loginStack(t)
			putInGroup(t, adm, "TestGroup", c.superuser, c.keysJSON)

			ran := false
			eng.GET("/admin/probe", adm.guard(c.checkKey), func(ic *inertia.Context) { ran = true })
			cookie := loginAndGetCookie(t, eng)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
			r.AddCookie(cookie)
			eng.ServeHTTP(w, r)

			if w.Code != c.wantCode {
				t.Errorf("status = %d, want %d", w.Code, c.wantCode)
			}
			if ran != c.wantRun {
				t.Errorf("handler ran = %v, want %v", ran, c.wantRun)
			}
		})
	}
}

// A user whose group was deleted from under them must be refused, not admitted.
func TestGuard_NoGroupIsForbidden(t *testing.T) {
	eng, adm := loginStack(t)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE admins SET group_id = NULL WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	eng.GET("/admin/probe", adm.guard("post.access"), func(ic *inertia.Context) {
		t.Error("handler must not run for a user with no group")
	})
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// The distinction the design turns on: a storage failure is a 500. Reporting it
// as 403 would make an outage look like a permissions decision, and the operator
// would go looking in the wrong place.
func TestGuard_DatabaseErrorIsInternalError(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.guard("post.access"), func(ic *inertia.Context) {
		t.Error("handler must not run when the group cannot be loaded")
	})
	cookie := loginAndGetCookie(t, eng)

	// Close the pool after logging in, so the session resolves but the group
	// query cannot.
	if err := adm.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — a storage failure is not a denial", w.Code)
	}
}

// The dashboard is exempt from authorisation, so a user holding nothing still
// reaches it. Its sidebar is filtered, which is the intended failure mode: a
// short menu rather than a wall.
func TestExemptRoute_ReachableWithoutPermissions(t *testing.T) {
	eng, adm := loginStack(t)
	putInGroup(t, adm, "Nobody", false, `[]`)

	ran := false
	eng.GET("/admin/exempt", adm.AuthMiddleware(), func(ic *inertia.Context) { ran = true })
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/exempt", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if !ran {
		t.Errorf("an exempt route must be reachable with no permissions; status %d", w.Code)
	}
}

// The registrar's whole purpose is that you cannot register a route without also
// permissioning it, and the tests above verify the two halves separately: the
// catalogue side of Handle, and guard driven through a hand-wired route. Neither
// would catch a Handle that recorded a key but forgot to pass the guard to the
// engine — so this one drives a request through a route the registrar itself
// wired, and asserts the handler stays unreached.
func TestRegistrar_WiredRouteIsEnforced(t *testing.T) {
	eng, adm := loginStack(t)
	putInGroup(t, adm, "TestGroup", false, `["other.modify"]`)

	ran := false
	adm.Resource(eng, "post").GET("/admin/post", func(ic *inertia.Context) { ran = true })
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/post", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — the registrar wired the route without a guard", w.Code)
	}
	if ran {
		t.Error("handler ran despite the group lacking post.access")
	}
}

// Mount must register everything, and the catalogue is how we check: the user
// and group resources contribute four keys between them, and a resource whose
// Mount call was forgotten shows up as a missing key rather than as a 404 nobody
// notices until a page is opened.
func TestMount_RegistersUsersGroupsAndAccount(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, &Config{Mount: "/admin"})
	if err := a.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	a.Mount(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}

	got := map[string]bool{}
	for _, p := range a.Permissions() {
		got[p.Key] = true
	}
	want := []string{"user.access", "user.modify", "group.access", "group.modify"}
	//goappctl:queue
	// Two resources, not one: changing a cron expression can make a job fire every
	// second, retrying one task cannot, so the permissions have to be separable.
	//
	// Reaching this line at all is the other half of the assertion. a is built with a
	// nil *app.Services, so a mount function that read a.Queue — a nil check, a call to
	// Kinds() — would panic here rather than in production. That is what keeps the
	// "mount unconditionally" rule enforced rather than merely documented.
	want = append(want, "task.access", "task.modify", "cron.access", "cron.modify")
	//goappctl:end
	for _, key := range want {
		if !got[key] {
			t.Errorf("catalogue missing %q — a resource was not mounted", key)
		}
	}

	// The account page is deliberately absent from the catalogue: it is exempt.
	for _, unwanted := range []string{"account.access", "account.modify"} {
		if got[unwanted] {
			t.Errorf("%q exists — the account route must stay exempt, or a user "+
				"with no permissions could never change their password", unwanted)
		}
	}
}
