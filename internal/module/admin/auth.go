package admin

import (
	"net/http"

	"github.com/millken/inertia"
)

// authMiddleware enforces login on the admin area. Non-admin routes and the
// login path pass through; everything else under the mount requires a truthy
// AuthKey in the session. On authenticated requests it injects the shared props
// admin pages need (menu, current user, mount, login path) before continuing.
func (m *Module) authMiddleware() inertia.HandlerFunc {
	mount := m.mount()
	login := m.loginPath()
	authKey := m.authKey()

	return func(c *inertia.Context) {
		if !isUnder(c.Request.URL.Path, mount) {
			c.Next()
			return
		}
		if c.Request.URL.Path == login {
			c.Next()
			return
		}
		sess := m.sessProv.Session(c)
		if v, ok := sess.Get(authKey); ok && v != nil && v != "" {
			c.Set("adminMenu", m.menuItems())
			c.Set("adminUser", v)
			c.Set("adminMount", mount)
			c.Set("loginPath", login)
			c.Next()
			return
		}
		// Not authenticated: redirect to login. inertia's ResponseWriter defers
		// WriteHeader until a Write, so emit a minimal body to flush the 302.
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

// isUnder reports whether path is prefix or a child segment of it, so "/admin"
// matches "/admin" and "/admin/x" but not "/adminfoo".
func isUnder(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '/'
}
