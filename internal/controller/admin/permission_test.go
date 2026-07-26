package admin

import (
	"net/http"
	"testing"
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
