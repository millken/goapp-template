package admin

import (
	"context"
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
