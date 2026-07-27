package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/millken/goapp-template/internal/service/session"
	"github.com/millken/inertia"
)

// LoginForm renders the public login page, or bounces an already-authenticated
// visitor to the dashboard — arriving here with a live session (back button,
// typed URL, stale bookmark) should not offer a second sign-in.
func (a *Admin) LoginForm(c *inertia.Context) {
	sess := a.Session.Session(c)
	if v, ok := sess.Get(a.authKey()); ok && v != nil && v != "" {
		// Authenticated — but send them to the dashboard only if their account
		// still works. A user whose account was disabled or deleted arrives here
		// *because* the admin area refused them, so redirecting on the strength
		// of the session alone would send them back to the page that refused
		// them: every admin page 403 or bounce, logout included, and the login
		// form never rendering. Showing the form instead is the way out.
		//
		// The extra lookup is confined to the login page, which is not the
		// authenticated request path the one-query-per-request rule is about.
		if !a.callerUnusable(c, v) {
			if err := c.Redirect(a.mount()); err != nil {
				slog.Error("admin login: redirect to dashboard", "err", err)
			}
			return
		}
	}

	c.Set("loginPath", a.LoginPath())
	token, err := sess.CSRFToken(c.Request.Context())
	if err != nil {
		slog.Error("admin login: csrf token", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Set("csrfToken", token)
	if err := c.Render("admin/login"); err != nil {
		slog.Error("render admin login", "err", err)
	}
}

// LoginSubmit authenticates against the users table. On success it sets the
// session auth key and redirects to the dashboard; on failure it re-renders
// login with a generic error. Save emits the cookie before the redirect body
// flushes (write-through writer).
func (a *Admin) LoginSubmit(c *inertia.Context) {
	sess := a.Session.Session(c)
	ip := a.clientIP(c.Request)
	if blocked, retry := a.loginBlocked(c.Request.Context(), ip); blocked {
		c.Status(http.StatusTooManyRequests)
		a.renderLogin(c, sess, fmt.Sprintf("尝试次数过多，请在 %d 分钟后重试。", int(retry.Minutes())+1))
		return
	}

	username := c.PostForm("username")
	password := c.PostForm("password")

	user, err := authenticate(c.Request.Context(), a.DB, a.usersTable(), username, password)
	if err != nil {
		if errors.Is(err, errAccountDisabled) {
			a.renderLogin(c, sess, "该账号已被禁用。")
			return
		}
		if !errors.Is(err, errInvalidCredentials) {
			slog.Error("admin login", "err", err) // db error, not a bad password
		}
		a.recordLoginFailure(c.Request.Context(), ip)
		a.renderLogin(c, sess, "用户名或密码不正确")
		return
	}

	// A new id for the authenticated session, before anything is written to it.
	// The login page minted a CSRF token, which means a session already existed,
	// and Store.Save keeps the id it is given — so without this, an id planted
	// before sign-in would still be valid after it.
	//
	// Fatal on failure by design: Regenerate deletes the old entry before saving
	// the new one, so an error means nothing moved and refusing is clean. Letting
	// the sign-in continue would be signing in to the id we meant to abandon.
	if err := sess.Regenerate(c.Request.Context()); err != nil {
		slog.Error("admin login: regenerate session", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	sess.Set(a.authKey(), user.ID)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("admin login: save session", "err", err)
	}
	a.clearLoginFailures(c.Request.Context(), ip)
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
func (a *Admin) callerUnusable(c *inertia.Context, v any) bool {
	id, ok := userID(v)
	if !ok {
		// An unreadable id is not a session anyone can use either.
		return true
	}
	cl, err := findCaller(c.Request.Context(), a.DB, a.usersTable(), id)
	switch {
	case errors.Is(err, errNoGroup):
		// findCaller cannot tell "deleted" from "no group" — its join drops
		// both — and neither can log in or out. Redirecting them to the
		// dashboard would bounce them straight back here: 403 on every admin
		// page, 403 on logout, and the login form never rendering. Clearing
		// cookies by hand would be the only way out.
		return true
	case err != nil:
		// A storage failure says nothing about this session. Keep the old
		// behaviour and let the dashboard report the outage.
		return false
	}
	return cl.status == statusDisabled
}

// renderLogin re-renders the login page carrying an error, with the CSRF token
// the form needs to be submittable again. Every path that shows the form after
// a refusal goes through here — three of them repeated the same six lines
// before, and a fourth that forgot the token would have rendered a form nobody
// could submit.
func (a *Admin) renderLogin(c *inertia.Context, sess session.Session, message string) {
	token, err := sess.CSRFToken(c.Request.Context())
	if err != nil {
		slog.Error("admin login: csrf token", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Set("loginPath", a.LoginPath())
	c.Set("csrfToken", token)
	c.Set("error", message)
	if err := c.Render("admin/login"); err != nil {
		slog.Error("render admin login", "err", err)
	}
}
