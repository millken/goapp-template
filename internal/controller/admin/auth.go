package admin

import (
	"encoding/json"
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

	id, ok := userID(v)
	if !ok {
		// The session holds whatever LoginSubmit stored; a value that is not a
		// number at all means a stale or tampered session rather than a
		// permission decision.
		slog.Error("admin auth: session user id is not a number", "value", v)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	g, err := findGroup(c.Request.Context(), a.DB, a.usersTable(), id)
	switch {
	case errors.Is(err, errNoGroup):
		// Fail closed. A user with no group has no permissions, and that is a
		// denial rather than an outage. Logged because the response is a bare
		// 403: without this line an operator whose user lost its group sees an
		// empty page and an empty log, with nothing to connect them.
		slog.Warn("admin auth: user has no permission group", "user", id)
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

// userID reads the id LoginSubmit stored back out of a session value. It cannot
// simply assert int64: the two session stores do not agree on types. store_memory
// returns values as written, but store_db — the production setting — marshals
// them to JSON, so an int64 comes back as a float64. session/flash.go documents
// the same divergence, and works around it by storing only flat strings.
func userID(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		// JSON's only number type. An id beyond 2^53 would already have lost
		// precision inside the session payload, so there is nothing to recover.
		return int64(n), true
	case int:
		return int64(n), true
	case json.Number:
		id, err := n.Int64()
		return id, err == nil
	default:
		return 0, false
	}
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
