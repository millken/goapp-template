package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
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

	cl, err := findCaller(c.Request.Context(), a.DB, a.usersTable(), id)
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
		slog.Error("admin auth: load caller", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	// A disabled account is neither of the above: not a permissions decision
	// about a resource, and not an outage. Bounce to login with an explanation —
	// a bare 403 would leave the user staring at an empty page with nothing to
	// act on.
	//
	// The session is deliberately NOT destroyed. A flash is session state
	// (session/flash.go keys it under _flash:), so destroying the session would
	// discard the message being staged, and Destroy also clears the response
	// cookie the message has to travel in — the two cannot both happen. Nor is
	// it needed: this function refuses the session on every request, so the
	// credential is already inert, and re-enabling the user restores their
	// session rather than forcing a fresh login.
	if cl.status == statusDisabled {
		sess.Flash("error", "该账号已被禁用。")
		if _, err := sess.Save(c.Request.Context()); err != nil {
			slog.Error("admin auth: stage disabled flash", "err", err, "user", id)
		}
		if err := c.Redirect(a.LoginPath()); err != nil {
			slog.Error("admin auth: redirect disabled user", "err", err)
		}
		c.Abort()
		return nil, false
	}

	c.Set("adminMenu", a.menuItems(cl.group))
	c.Set("adminUser", map[string]any{"id": id, "username": cl.username})
	c.Set("adminMount", a.mount())
	c.Set("loginPath", a.LoginPath())
	c.Set("currentPath", c.Request.URL.Path)

	//goappctl:storage
	// One prop, read by one component (ImagePicker), set here rather than in
	// the handlers that render it. resolve is the only place already holding
	// the caller's group: a page handler would have to look it up again, and
	// the admin area's rule is one query per request. Deriving it client-side
	// from adminMenu was the alternative and was rejected — it would make the
	// picker's behaviour depend on a sidebar entry existing.
	c.Set("canBrowseFiles", cl.group.Superuser ||
		cl.group.Permissions.Allows("filemanager"+verbAccess))
	//goappctl:end

	// Every admin page renders at least the shell's logout form, so every admin
	// page needs a token. resolve is the one place they all pass through.
	token, err := a.Session.Session(c).CSRFToken(c.Request.Context())
	if err != nil {
		slog.Error("admin auth: csrf token", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}
	c.Set("csrfToken", token)
	return cl.group, true
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
		// JSON's only number type. A non-integral value is not an id this code
		// ever wrote, so refuse it rather than truncate it into a valid one. An
		// id beyond 2^53 would already have lost precision inside the session
		// payload, so there is nothing to recover there.
		if n != math.Trunc(n) {
			return 0, false
		}
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
// permission. It is what the three exempt routes use: logout, the dashboard —
// which must stay reachable, or a user with no permissions logs in, sees only
// 403, and cannot self-diagnose — and the account password page, which cannot be
// permission-gated without making it impossible for such a user to fix their own
// credentials. Every other admin route goes through the registrar.
func (a *Admin) AuthMiddleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		if _, ok := a.resolve(c); !ok {
			return
		}
		c.Next()
	}
}
