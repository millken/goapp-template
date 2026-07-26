package admin

import (
	"errors"
	"log/slog"

	"github.com/millken/inertia"
)

// LoginForm renders the public login page, or bounces an already-authenticated
// visitor to the dashboard — arriving here with a live session (back button,
// typed URL, stale bookmark) should not offer a second sign-in.
func (a *Admin) LoginForm(c *inertia.Context) {
	sess := a.Session.Session(c)
	if v, ok := sess.Get(a.authKey()); ok && v != nil && v != "" {
		if err := c.Redirect(a.mount()); err != nil {
			slog.Error("admin login: redirect to dashboard", "err", err)
		}
		return
	}

	c.Set("loginPath", a.LoginPath())
	if err := c.Render("admin/login"); err != nil {
		slog.Error("render admin login", "err", err)
	}
}

// LoginSubmit authenticates against the users table. On success it sets the
// session auth key and redirects to the dashboard; on failure it re-renders
// login with a generic error. Save emits the cookie before the redirect body
// flushes (write-through writer).
func (a *Admin) LoginSubmit(c *inertia.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := authenticate(c.Request.Context(), a.DB, a.usersTable(), username, password)
	if err != nil {
		if errors.Is(err, errAccountDisabled) {
			c.Set("loginPath", a.LoginPath())
			c.Set("error", "该账号已被禁用。")
			if rerr := c.Render("admin/login"); rerr != nil {
				slog.Error("render admin login", "err", rerr)
			}
			return
		}
		if !errors.Is(err, errInvalidCredentials) {
			slog.Error("admin login", "err", err) // db error, not a bad password
		}
		c.Set("loginPath", a.LoginPath())
		c.Set("error", "invalid username or password")
		if rerr := c.Render("admin/login"); rerr != nil {
			slog.Error("render admin login", "err", rerr)
		}
		return
	}

	sess := a.Session.Session(c)
	sess.Set(a.authKey(), user.ID)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("admin login: save session", "err", err)
	}
	if err := c.Redirect(a.mount()); err != nil {
		slog.Error("admin login: redirect to dashboard", "err", err)
	}
}

// Logout destroys the session and redirects to login.
func (a *Admin) Logout(c *inertia.Context) {
	sess := a.Session.Session(c)
	if err := sess.Destroy(c.Request.Context()); err != nil {
		slog.Error("admin logout", "err", err)
	}
	if err := c.Redirect(a.LoginPath()); err != nil {
		slog.Error("admin logout: redirect to login", "err", err)
	}
}

// Dashboard renders the admin home (adminMenu/adminUser are injected by the auth middleware).
func (a *Admin) Dashboard(c *inertia.Context) {
	if err := c.Render("admin/dashboard"); err != nil {
		slog.Error("render admin dashboard", "err", err)
	}
}
