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
		// Authenticated — but send them to the dashboard only if their account
		// still works. A disabled user arrives here *because* resolve bounced
		// them, so redirecting on the strength of the session alone would send
		// them back to the page that bounced them: an endless loop in which the
		// explanation resolve staged is never rendered. Rendering the form
		// instead ends the loop and lets the flash through.
		//
		// The extra lookup is confined to the login page, which is not the
		// authenticated request path the one-query-per-request rule is about.
		if !a.callerDisabled(c, v) {
			if err := c.Redirect(a.mount()); err != nil {
				slog.Error("admin login: redirect to dashboard", "err", err)
			}
			return
		}
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

// callerDisabled reports whether the session's user exists and is disabled.
// Anything else — an unreadable id, a missing user, a storage failure — reports
// false, so the only behaviour this can change is ending the redirect loop for a
// user who really is disabled.
func (a *Admin) callerDisabled(c *inertia.Context, v any) bool {
	id, ok := userID(v)
	if !ok {
		return false
	}
	cl, err := findCaller(c.Request.Context(), a.DB, a.usersTable(), id)
	if err != nil {
		return false
	}
	return cl.status == statusDisabled
}
