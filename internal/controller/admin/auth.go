package admin

import (
	"log/slog"

	"github.com/millken/inertia"
)

// AuthMiddleware enforces login on the routes it guards. Because it is attached
// only to protected routes, it never filters by path. On success it injects the
// shared props admin pages need (menu, user, mount, login path).
func (a *Admin) AuthMiddleware() inertia.HandlerFunc {
	mount := a.mount()
	login := a.LoginPath()
	authKey := a.authKey()

	return func(c *inertia.Context) {
		sess := a.Session.Session(c)
		if v, ok := sess.Get(authKey); ok && v != nil && v != "" {
			c.Set("adminMenu", a.menuItems())
			c.Set("adminUser", v)
			c.Set("adminMount", mount)
			c.Set("loginPath", login)
			c.Next()
			return
		}
		// Not authenticated: redirect to login. Under PJAX this becomes a
		// {redirect} payload rather than a 302 — see inertia's Context.Redirect.
		if err := c.Redirect(login); err != nil {
			slog.Error("admin auth: redirect to login", "err", err)
		}
		c.Abort()
	}
}
