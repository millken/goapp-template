package admin

import (
	"net/http"

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
		// Not authenticated: redirect to login.
		redirectTo(c, login)
		c.Abort()
	}
}

// redirectTo writes a 302 to location. A minimal body is written because
// inertia's write-through ResponseWriter flushes the header on first Write.
func redirectTo(c *inertia.Context, location string) {
	c.Writer.Header().Set("Location", location)
	c.Status(http.StatusFound)
	_, _ = c.Writer.Write([]byte("redirecting..."))
}
