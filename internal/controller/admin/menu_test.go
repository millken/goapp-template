package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/millken/inertia"
)

// The convention is "empty resource means always show". Asserted on menuEntry
// directly: AddMenuItem is only one way to produce an empty resource, so testing
// it instead would leave the convention itself unpinned.
func TestMenuItems_EmptyResourceAlwaysShows(t *testing.T) {
	a := New(nil, nil)
	a.menu = []menuEntry{{item: MenuItem{Title: "Docs", Path: "/admin/docs"}}}

	for _, g := range []*group{
		{Permissions: permSet{}},
		{Superuser: true},
	} {
		got := a.menuItems(g)
		if len(got) != 1 || got[0].Title != "Docs" {
			t.Errorf("group %+v: an entry with no resource must always show, got %+v", g, got)
		}
	}
}

func TestMenuItems_FiltersByAccess(t *testing.T) {
	a := New(nil, nil)
	a.menu = []menuEntry{
		{item: MenuItem{Title: "Post", Path: "/admin/post"}, resource: "post"},
		{item: MenuItem{Title: "User", Path: "/admin/user"}, resource: "user"},
	}

	only := a.menuItems(&group{Permissions: permSet{"post.access": true}})
	if len(only) != 1 || only[0].Title != "Post" {
		t.Errorf("want only Post, got %+v", only)
	}

	// modify implies access, so an entry is reachable through either key.
	viaModify := a.menuItems(&group{Permissions: permSet{"user.modify": true}})
	if len(viaModify) != 1 || viaModify[0].Title != "User" {
		t.Errorf("user.modify should surface the User entry, got %+v", viaModify)
	}

	all := a.menuItems(&group{Superuser: true})
	if len(all) != 2 {
		t.Errorf("a superuser sees everything, got %+v", all)
	}

	none := a.menuItems(&group{Permissions: permSet{}})
	if len(none) != 0 {
		t.Errorf("no permissions means no resource entries, got %+v", none)
	}
}

func TestAddMenuItem_ProducesAnEmptyResource(t *testing.T) {
	a := New(nil, nil)
	a.AddMenuItem(MenuItem{Title: "Hand-written", Path: "/admin/x"})
	if len(a.menu) != 1 || a.menu[0].resource != "" {
		t.Errorf("AddMenuItem should leave resource empty, got %+v", a.menu)
	}
}

func TestAddResourceMenuItem_RecordsTheResource(t *testing.T) {
	a := New(nil, nil)
	a.addResourceMenuItem(MenuItem{Title: "Post", Path: "/admin/post"}, "post")
	if len(a.menu) != 1 || a.menu[0].resource != "post" {
		t.Errorf("want resource post, got %+v", a.menu)
	}
}

// Sections display in first-registration order — not alphabetically — so the
// wiring order in serve.go is the one knob controlling the rail. Items inside
// a section keep the Order-then-Title rule.
func TestMenuItems_SectionOrderFollowsRegistration(t *testing.T) {
	a := New(nil, nil)
	a.AddMenuItem(MenuItem{Title: "Settings", Path: "/admin/settings", Section: "System"})
	a.AddMenuItem(MenuItem{Title: "Groups", Path: "/admin/group", Section: "Access"})
	a.AddMenuItem(MenuItem{Title: "Users", Path: "/admin/user", Section: "Access"})
	a.AddMenuItem(MenuItem{Title: "Posts", Path: "/admin/post"}) // empty → "Content"

	got := a.menuItems(&group{Superuser: true})
	titles := make([]string, len(got))
	for i, m := range got {
		titles[i] = m.Section + ":" + m.Title
	}
	want := "System:Settings,Access:Groups,Access:Users,Content:Posts"
	if s := strings.Join(titles, ","); s != want {
		t.Errorf("menu = %q, want %q", s, want)
	}
}

// The spec asks for the menu to be asserted on both an exempt route and a guarded
// one, because the two middlewares have to agree: they inject the same prop, and
// they only agree because both go through resolve. menuItems is tested in
// isolation above; this reads the prop off a real request through each path.
func TestAdminMenuPropIsFilteredOnBothMiddlewares(t *testing.T) {
	eng, adm := loginStack(t)
	// Reachable, gated, and ungated — one of each, so filtering has something to
	// keep and something to drop.
	adm.addResourceMenuItem(MenuItem{Title: "Post", Path: "/admin/post"}, "post")
	adm.addResourceMenuItem(MenuItem{Title: "Billing", Path: "/admin/billing"}, "billing")
	adm.AddMenuItem(MenuItem{Title: "Docs", Path: "/admin/docs"})
	putInGroup(t, adm, "Editors", false, `["post.access"]`)

	// Each route reports the menu the middleware injected, so the assertion is on
	// what a page would actually receive.
	probe := func(ic *inertia.Context) {
		v, ok := ic.Get("adminMenu")
		if !ok {
			t.Error("adminMenu prop was not injected")
			return
		}
		titles := make([]string, 0, 3)
		for _, m := range v.([]MenuItem) {
			titles = append(titles, m.Title)
		}
		if got := strings.Join(titles, ","); got != "Docs,Post" {
			t.Errorf("%s: adminMenu = %q, want \"Docs,Post\"", ic.Request.URL.Path, got)
		}
	}
	eng.GET("/admin/exempt", adm.AuthMiddleware(), probe)
	eng.GET("/admin/post", adm.guard("post.access"), probe)
	cookie := loginAndGetCookie(t, eng)

	for _, path := range []string{"/admin/exempt", "/admin/post"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(cookie)
		eng.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
		}
	}
}

// The rail must not reorder itself per user. Section order comes from the
// unfiltered menu, so hiding a section's first-registered item leaves the
// section where it was. Deriving the order from the filtered slice instead
// would rank sections by whichever still had a visible item earliest — which is
// why "Content" is registered between Access's hidden and visible items: under
// that bug the filtered menu would lead with Content. A superuser cannot detect
// the difference, since nothing is filtered for them.
func TestMenuItems_SectionOrderSurvivesFiltering(t *testing.T) {
	a := New(nil, nil)
	a.addResourceMenuItem(MenuItem{Title: "Groups", Path: "/admin/group", Section: "Access"}, "group")
	a.AddMenuItem(MenuItem{Title: "Posts", Path: "/admin/post", Section: "Content"})
	a.AddMenuItem(MenuItem{Title: "Audit", Path: "/admin/audit", Section: "Access"})

	sections := func(g *group) string {
		var out []string
		for _, m := range a.menuItems(g) {
			out = append(out, m.Section+":"+m.Title)
		}
		return strings.Join(out, ",")
	}

	// Within a section, Order-then-Title still applies: Audit sorts before Groups.
	if got, want := sections(&group{Superuser: true}), "Access:Audit,Access:Groups,Content:Posts"; got != want {
		t.Errorf("superuser menu = %q, want %q", got, want)
	}
	// Access keeps first place even though the entry that earned it is hidden.
	if got, want := sections(&group{Permissions: permSet{}}), "Access:Audit,Content:Posts"; got != want {
		t.Errorf("filtered menu = %q, want %q", got, want)
	}
}
