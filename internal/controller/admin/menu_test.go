package admin

import "testing"

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
