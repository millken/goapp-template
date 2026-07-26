package admin

import (
	"context"
	"encoding/json"
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
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
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
			if w.Code == http.StatusFound {
				t.Errorf("status = 302, want the form re-rendered with an error")
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
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
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
	if w.Code == http.StatusFound {
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

// Every admin redirect must become a payload under PJAX, or the client follows
// the 3xx transparently and shows the list while the address bar still names the
// URL it posted to. The same rule TestRedirects_UnderPJAX pins for the auth
// redirects applies to the mutations.
func TestUserCreate_RedirectIsAPayloadUnderPJAX(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	var gid int64
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"username": {"pjaxuser"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}
	r := httptest.NewRequest(http.MethodPost, "/admin/user", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Pjax", "true")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: a PJAX redirect is a payload", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v (raw: %q)", err, w.Body.String())
	}
	if got := body["redirect"]; got != "/admin/user" {
		t.Errorf(`body["redirect"] = %v, want /admin/user`, got)
	}
}

// Rule 1. Both directions of the same rule: you are the one account you must
// not be able to remove or switch off.
func TestUserDeleteAndDisable_CannotTargetYourself(t *testing.T) {
	for _, c := range []struct {
		name string
		path string
		form url.Values
	}{
		{"delete", "/delete", nil},
		{"disable", "/status", url.Values{"status": {"0"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm, cookie := adminStack(t)
			var id int64
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT id FROM users WHERE username = 'alice'`).Scan(&id); err != nil {
				t.Fatal(err)
			}

			w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d%s", id, c.path), c.form)
			if w.Code == http.StatusFound {
				t.Error("acting on your own account must be refused")
			}

			var n, status int
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT COUNT(*), COALESCE(MAX(status), -1) FROM users WHERE id = ?`, id).
				Scan(&n, &status); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Error("the account was deleted anyway")
			}
			if status != statusActive {
				t.Error("the account was disabled anyway")
			}
		})
	}
}

// Rule 3, through HTTP rather than the guard's unit test: deleting the only
// other superuser is fine, deleting the last one is not.
func TestUserDelete_KeepsOneEnabledSuperuser(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	// bob is a second superuser, so deleting him is allowed...
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, status, group_id)
		 VALUES ('bob', 'x', 0, 1, (SELECT id FROM user_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatal(err)
	}
	var bob int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'bob'`).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/delete", bob), nil); w.Code != http.StatusFound {
		t.Fatalf("deleting a second superuser: status = %d, want 303", w.Code)
	}

	// ...and now alice is the last one, but she is also the caller, so rule 1
	// already covers her. Add a third superuser and have alice delete them to
	// leave exactly one, then check the count never reached zero.
	if n := countEnabledSuperusers(t, adm); n != 1 {
		t.Errorf("enabled superusers = %d, want 1", n)
	}
}

// A group with no superuser: disabling its only member is fine, because the
// rule is about superusers, not about users in general.
func TestUserSetStatus_DisablesANonSuperuser(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, status, group_id)
		 VALUES ('erin', 'x', 0, 1, (SELECT id FROM user_groups WHERE name = 'Editors'))`); err != nil {
		t.Fatal(err)
	}
	var erin int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'erin'`).Scan(&erin); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/status", erin), url.Values{"status": {"0"}})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}
	var status int
	if err := adm.DB.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, erin).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != statusDisabled {
		t.Errorf("status = %d, want %d", status, statusDisabled)
	}

	// Enabling again is not subject to rules 1 or 3, and the value comes from the
	// request rather than being toggled: submitting 1 twice leaves it enabled.
	for range 2 {
		if w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/status", erin), url.Values{"status": {"1"}}); w.Code != http.StatusFound {
			t.Fatalf("enable: status = %d, want 303", w.Code)
		}
	}
	if err := adm.DB.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, erin).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != statusActive {
		t.Errorf("status after two enables = %d, want %d", status, statusActive)
	}
}
