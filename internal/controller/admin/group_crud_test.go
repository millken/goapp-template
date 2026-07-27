package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/millken/inertia"
)

// groupStack mounts both resources, since the group tests need users too.
func groupStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountUsers(eng)
	adm.mountGroups(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

// A group with members cannot be deleted: a user with no group is refused
// everything, including logout, which is the state stage 1 spent effort fixing.
// The message has to name the count, or the operator has no idea what to do next.
func TestGroupDelete_RefusedWhileItHasMembers(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d/delete", gid), nil)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect carrying the refusal", w.Code)
	}
	var n int
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_groups WHERE id = ?`, gid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the group was deleted despite having a member")
	}
}

func TestGroupDelete_SucceedsWhenEmpty(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Empty', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Empty'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	if w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d/delete", gid), nil); w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	var n int
	if err := adm.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_groups WHERE id = ?`, gid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("an empty group was not deleted")
	}
}

// Clearing the superuser flag on the only superuser group strands everyone, and
// the guard must roll it back rather than merely reporting an error.
func TestGroupUpdate_CannotClearTheLastSuperuserFlag(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d", gid), url.Values{
		"name": {"Administrators"},
		// superuser checkbox absent = unchecked
	})
	if w.Code == http.StatusFound {
		t.Error("clearing the last superuser flag must be refused")
	}
	var superuser int
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT superuser FROM user_groups WHERE id = ?`, gid).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if superuser != 1 {
		t.Error("the flag was cleared anyway — the change was not rolled back")
	}
}

func TestGroupCreate_ValidatesTheName(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	for _, c := range []struct{ name, value string }{
		{"blank", ""},
		{"too short", "a"},
		{"taken", "Administrators"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := post(t, eng, cookie, "/admin/group", url.Values{"name": {c.value}})
			if w.Code == http.StatusFound {
				t.Error("want the form re-rendered with an error")
			}
		})
	}
	var n int
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_groups`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("groups = %d, want 1 — a rejected submit created a row", n)
	}
}

// modify implies access one-way, exactly as permSet.Allows has it. The grid does
// this client-side for convenience; the server does it again because the client
// cannot be the enforcement point.
func TestGroupUpdate_NormalisesModifyImpliesAccess(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Editors'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	// groupStack only mounts the "user" and "group" resources (see groupStack),
	// so "user.modify" is the real catalogue key available here — a "post.modify"
	// submission would be dropped as unknown, which is the behaviour the next
	// test exercises directly. Only user.modify submitted — user.access must be
	// stored too.
	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d", gid), url.Values{
		"name":        {"Editors"},
		"permissions": {"user.modify"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var raw string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT permissions FROM user_groups WHERE id = ?`, gid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if !got["user.modify"] || !got["user.access"] {
		t.Errorf("stored %v, want both user.modify and user.access", keys)
	}
}

// A permission key that arrives in a POST but is not in the catalogue must not
// be stored — the catalogue, not the submitted form, decides what is trusted.
func TestGroupUpdate_UnknownKeyNotStored(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors2', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Editors2'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	// "post" registers no routes anywhere in this stack, so post.modify is not
	// in the catalogue: a hand-made POST naming it must not be trusted.
	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d", gid), url.Values{
		"name":        {"Editors2"},
		"permissions": {"post.modify", "user.access"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var raw string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT permissions FROM user_groups WHERE id = ?`, gid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if got["post.modify"] {
		t.Errorf("stored %v, want post.modify dropped — it is not in the catalogue", keys)
	}
	if !got["user.access"] {
		t.Errorf("stored %v, want user.access kept — it is in the catalogue", keys)
	}
}

// A key for a resource that no longer registers routes is shown to the operator
// and removed on save. Writing it back silently would leave permissions in
// effect that the interface never displayed.
func TestPermissionRows_SeparatesStaleKeys(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	a.Resource(eng, "post").GET("/admin/post", func(c *inertia.Context) {})
	a.Resource(eng, "post").POST("/admin/post", func(c *inertia.Context) {})

	rows, stale := a.permissionRows([]string{"post.access", "billing.modify"})

	if len(rows) != 1 || rows[0].Resource != "post" {
		t.Fatalf("rows = %+v, want one row for post", rows)
	}
	if !rows[0].Access || rows[0].Modify {
		t.Errorf("post row = %+v, want access only", rows[0])
	}
	if len(stale) != 1 || stale[0] != "billing.modify" {
		t.Errorf("stale = %v, want [billing.modify]", stale)
	}
}
