package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/millken/inertia"
)

// resolve authenticates the request, loads the caller's group, and injects the
// props every admin page expects — including the menu, filtered to what the
// caller may reach. It reports false when it has already written a response
// (redirect to login, 403, or 500), in which case the caller must stop.
//
// Both middlewares go through here, so the dashboard (exempt from
// authorisation) still gets the same filtered menu a guarded page gets. It is
// also why the group lookup is not confined to guard.
func (a *Admin) resolve(c *inertia.Context) (*group, bool) {
	sess := a.Session.Session(c)
	v, ok := sess.Get(a.authKey())
	if !ok || v == nil || v == "" {
		// Not authenticated: redirect to login. Under PJAX this becomes a
		// {redirect} payload rather than a 302 — see inertia's Context.Redirect.
		if err := c.Redirect(a.LoginPath()); err != nil {
			slog.Error("admin auth: redirect to login", "err", err)
		}
		c.Abort()
		return nil, false
	}

	id, ok := v.(int64)
	if !ok {
		// The session holds whatever LoginSubmit stored; a different type means
		// a stale or tampered session rather than a permission decision.
		slog.Error("admin auth: session user id is not an int64", "value", v)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	g, err := findGroup(c.Request.Context(), a.DB, a.usersTable(), id)
	switch {
	case errors.Is(err, errNoGroup):
		// Fail closed. A user with no group has no permissions, and that is a
		// denial rather than an outage.
		c.AbortWithStatus(http.StatusForbidden)
		return nil, false
	case err != nil:
		// A storage problem must not read as an authorisation decision.
		slog.Error("admin auth: load group", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	c.Set("adminMenu", a.menuItems(g))
	c.Set("adminUser", v)
	c.Set("adminMount", a.mount())
	c.Set("loginPath", a.LoginPath())
	return g, true
}

// AuthMiddleware enforces login on the routes it guards, without requiring any
// permission. It is what the exempt routes use: logout, and the dashboard —
// which must stay reachable, or a user with no permissions logs in, sees only
// 403, and cannot self-diagnose.
func (a *Admin) AuthMiddleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		if _, ok := a.resolve(c); !ok {
			return
		}
		c.Next()
	}
}
