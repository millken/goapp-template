package admin

import (
	"errors"
	"log/slog"

	"github.com/millken/inertia"
)

// loginForm renders the public login page.
func (m *Module) loginForm(c *inertia.Context) {
	c.Set("loginPath", m.loginPath())
	if err := c.Render("admin/login"); err != nil {
		slog.Error("render admin login", "err", err)
	}
}

// loginSubmit authenticates username+password against the users table. On
// success it sets the session auth key and redirects to the dashboard; on
// failure it re-renders the login page with a generic error. The session cookie
// is emitted by Save before the redirect body flushes (write-through writer).
func (m *Module) loginSubmit(c *inertia.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := authenticate(c.Request.Context(), m.dbProv.DB(), m.usersTable(), username, password)
	if err != nil {
		if !errors.Is(err, errInvalidCredentials) {
			slog.Error("admin login", "err", err) // db error, not a bad password
		}
		c.Set("loginPath", m.loginPath())
		c.Set("error", "invalid username or password")
		if rerr := c.Render("admin/login"); rerr != nil {
			slog.Error("render admin login", "err", rerr)
		}
		return
	}

	sess := m.sessProv.Session(c)
	sess.Set(m.authKey(), user.ID)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("admin login: save session", "err", err)
	}
	redirectTo(c, m.mount())
}

// logout destroys the session (clearing its cookie) and redirects to login.
func (m *Module) logout(c *inertia.Context) {
	sess := m.sessProv.Session(c)
	if err := sess.Destroy(c.Request.Context()); err != nil {
		slog.Error("admin logout", "err", err)
	}
	redirectTo(c, m.loginPath())
}

// dashboard renders the admin home. adminMenu/adminUser are already injected by
// the auth middleware.
func (m *Module) dashboard(c *inertia.Context) {
	if err := c.Render("admin/dashboard"); err != nil {
		slog.Error("render admin dashboard", "err", err)
	}
}
