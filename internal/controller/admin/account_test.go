package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/millken/inertia"
)

func accountStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountAccount(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

// The whole reason this route is exempt: a user whose group grants nothing must
// still be able to change their own password. If it went through the registrar
// they never could.
func TestAccountPassword_ReachableWithoutAnyPermission(t *testing.T) {
	eng, adm, cookie := accountStack(t)
	putInGroup(t, adm, "Nobody", false, `[]`)

	r := httptest.NewRequest(http.MethodGet, "/admin/account/password", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a permission-less user must reach this page", w.Code)
	}
}

func TestAccountPassword_ChangesThePassword(t *testing.T) {
	eng, adm, cookie := accountStack(t)
	ctx := context.Background()

	w := post(t, eng, cookie, "/admin/account/password", url.Values{
		"current":  {"pw"},
		"password": {"newpassword"},
		"confirm":  {"newpassword"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var hash string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "newpassword") {
		t.Error("the new password does not verify")
	}
	if verifyPassword(hash, "pw") {
		t.Error("the old password still verifies")
	}
}

func TestAccountPassword_Rejections(t *testing.T) {
	for _, c := range []struct {
		name string
		form url.Values
	}{
		{"wrong current password", url.Values{"current": {"nope"}, "password": {"newpassword"}, "confirm": {"newpassword"}}},
		{"confirmation mismatch", url.Values{"current": {"pw"}, "password": {"newpassword"}, "confirm": {"different"}}},
		{"too short", url.Values{"current": {"pw"}, "password": {"short"}, "confirm": {"short"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm, cookie := accountStack(t)
			w := post(t, eng, cookie, "/admin/account/password", c.form)
			// A field error, not a 403: the user is allowed here, they just got
			// something wrong.
			if w.Code == http.StatusFound {
				t.Error("want the form re-rendered with an error")
			}
			if w.Code == http.StatusForbidden {
				t.Error("a wrong current password is a field error, not a refusal to be here")
			}
			var hash string
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if !verifyPassword(hash, "pw") {
				t.Error("the password changed despite the rejection")
			}
		})
	}
}
