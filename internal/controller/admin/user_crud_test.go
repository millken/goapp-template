package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/millken/inertia"
)

// adminStack is loginStack plus the user and group routes mounted, which is what
// every test below needs.
func adminStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountUsers(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

func post(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	return w
}

func TestUserCreate_StoresAHashedPasswordAndTheGroup(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()

	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, "/admin/user", url.Values{
		"username": {"carol"},
		"password": {"s3cretpw"},
		"group_id": {fmt.Sprint(gid)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", w.Code, w.Body.String())
	}

	var hash string
	var status int
	var got int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash, status, group_id FROM users WHERE username = 'carol'`).
		Scan(&hash, &status, &got); err != nil {
		t.Fatalf("carol was not created: %v", err)
	}
	if hash == "s3cretpw" {
		t.Error("the password was stored in plaintext")
	}
	if !verifyPassword(hash, "s3cretpw") {
		t.Error("the stored hash does not verify the submitted password")
	}
	if status != statusActive {
		t.Errorf("status = %d, want %d", status, statusActive)
	}
	if got != gid {
		t.Errorf("group_id = %d, want %d", got, gid)
	}
}

func TestUserCreate_RejectsBadInput(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	var gid int64
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		form url.Values
	}{
		{"password too short", url.Values{"username": {"dave"}, "password": {"short"}, "group_id": {fmt.Sprint(gid)}}},
		{"username taken", url.Values{"username": {"alice"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"username illegal", url.Values{"username": {"has space"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"username too short", url.Values{"username": {"ab"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"group missing", url.Values{"username": {"dave"}, "password": {"s3cretpw"}, "group_id": {"99999"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := post(t, eng, cookie, "/admin/user", c.form)
			// A rejected submit re-renders the form rather than redirecting.
			if w.Code == http.StatusSeeOther {
				t.Errorf("status = 303, want the form re-rendered with an error")
			}
			var n int
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT COUNT(*) FROM users WHERE username = ?`, c.form.Get("username")).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if c.form.Get("username") != "alice" && n != 0 {
				t.Errorf("a rejected submit created %d row(s)", n)
			}
		})
	}
}

// A blank password on update means "leave it alone" — otherwise every edit of a
// username would silently reset the person's password.
func TestUserUpdate_BlankPasswordKeepsTheOldOne(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()

	var id, gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id, group_id FROM users WHERE username = 'alice'`).Scan(&id, &gid); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d", id), url.Values{
		"username": {"alice2"},
		"password": {""},
		"group_id": {fmt.Sprint(gid)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", w.Code, w.Body.String())
	}

	var after, name string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash, username FROM users WHERE id = ?`, id).Scan(&after, &name); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Error("a blank password field changed the stored hash")
	}
	if name != "alice2" {
		t.Errorf("username = %q, want alice2", name)
	}
}

// Rule 2: editing your own username and password is fine; moving yourself to
// another group is not.
func TestUserUpdate_CannotChangeYourOwnGroup(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var id, other int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'alice'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Editors'`).Scan(&other); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d", id), url.Values{
		"username": {"alice"},
		"password": {""},
		"group_id": {fmt.Sprint(other)},
	})
	if w.Code == http.StatusSeeOther {
		t.Error("changing your own group must be refused")
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT group_id FROM users WHERE id = ?`, id).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	if gid == other {
		t.Error("the group changed anyway")
	}
}
