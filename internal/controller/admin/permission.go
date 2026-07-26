package admin

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/millken/inertia"
)

// Permission verbs. A route's verb comes from its HTTP method, so nothing has to
// name it: reading is access, changing anything is modify.
const (
	verbAccess = ".access"
	verbModify = ".modify"
)

// permKey is the permission a method implies for a resource. Only GET and HEAD
// read; anything else is treated as a change, so a method nobody anticipated
// errs toward requiring more permission rather than less.
func permKey(resource, method string) string {
	switch method {
	case http.MethodGet, http.MethodHead:
		return resource + verbAccess
	default:
		return resource + verbModify
	}
}

// permSet is one group's permissions, keyed for O(1) lookup.
type permSet map[string]bool

// Allows reports whether the set permits key. modify implies access — a group
// that may change a resource may obviously read it — but not the reverse.
func (p permSet) Allows(key string) bool {
	if p[key] {
		return true
	}
	if resource, isAccess := strings.CutSuffix(key, verbAccess); isAccess {
		return p[resource+verbModify]
	}
	return false
}

// Permission is one catalogue entry: a key and the routes it guards. The routes
// are recorded so a management screen can explain a permission rather than
// showing a bare string.
type Permission struct {
	Key    string   `json:"key"`
	Routes []string `json:"routes"`
}

// recordPermission adds a route to the catalogue under key. Called during
// startup wiring only, so no locking is needed — same contract as AddMenuItem.
func (a *Admin) recordPermission(key, method, path string) {
	route := method + " " + path
	if a.perms == nil {
		a.perms = map[string][]string{}
	}
	if slices.Contains(a.perms[key], route) {
		return
	}
	a.perms[key] = append(a.perms[key], route)
}

// Permissions returns every registered key with the routes it guards, sorted by
// key so a UI built on it is stable.
func (a *Admin) Permissions() []Permission {
	out := make([]Permission, 0, len(a.perms))
	for key, routes := range a.perms {
		out = append(out, Permission{Key: key, Routes: slices.Clone(routes)})
	}
	slices.SortFunc(out, func(x, y Permission) int { return strings.Compare(x.Key, y.Key) })
	return out
}

// Registrar registers one resource's admin routes. Every route it registers is
// guarded by that resource's permission, so a route cannot be added without its
// check — which is the failure mode of hand-written permission calls.
type Registrar struct {
	admin    *Admin
	eng      *inertia.Engine
	resource string
}

// Resource returns a registrar for name's routes. name is the permission prefix,
// so "post" yields post.access and post.modify. It is declared rather than
// derived from the path: deriving it would silently re-key every permission when
// someone changes a mount path, revoking access with no error anywhere.
func (a *Admin) Resource(eng *inertia.Engine, name string) *Registrar {
	// permKey and Allows build and split keys textually around ".access" and
	// ".modify", so a name containing a dot would produce keys that alias each
	// other in confusing ways. Rejecting it here is the one place the name enters
	// the system, and a bad name is a wiring mistake — same class as the panic in
	// Handle, and caught at startup rather than in a request.
	if !resourceNameRe.MatchString(name) {
		panic("admin: Resource: illegal resource name " + strconv.Quote(name) +
			" (want ^[a-z0-9][a-z0-9_-]*$)")
	}
	return &Registrar{admin: a, eng: eng, resource: name}
}

// resourceNameRe keeps a resource name free of the separators permission keys are
// built from.
var resourceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Handle registers path for method, guarded by the permission that method
// implies, and records the pairing in the catalogue.
//
// inertia.Engine exposes one method per verb and keeps addRoute unexported, so
// this dispatches rather than passing the method through. An unsupported method
// panics: it can only come from a caller in this repository, and a route silently
// not registered would serve 404 with no clue why.
func (r *Registrar) Handle(method, path string, h inertia.HandlerFunc) {
	key := permKey(r.resource, method)
	guard := r.admin.guard(key)

	// guard alone, never stacked with AuthMiddleware: guard does everything
	// AuthMiddleware does plus the key check, and stacking both would resolve
	// the caller's group twice per request.
	switch method {
	case http.MethodGet:
		r.eng.GET(path, guard, h)
	case http.MethodPost:
		r.eng.POST(path, guard, h)
	case http.MethodPut:
		r.eng.PUT(path, guard, h)
	case http.MethodPatch:
		r.eng.PATCH(path, guard, h)
	case http.MethodDelete:
		r.eng.DELETE(path, guard, h)
	default:
		panic("admin: Registrar.Handle: unsupported method " + method)
	}
	r.admin.recordPermission(key, method, path)
}

// GET registers a read route, guarded by <resource>.access.
func (r *Registrar) GET(path string, h inertia.HandlerFunc) {
	r.Handle(http.MethodGet, path, h)
}

// POST registers a write route, guarded by <resource>.modify.
func (r *Registrar) POST(path string, h inertia.HandlerFunc) {
	r.Handle(http.MethodPost, path, h)
}

// Menu adds the resource's sidebar entry under section (the icon-rail group),
// shown only to callers holding the resource's access key.
func (r *Registrar) Menu(section, title, path string) {
	r.admin.addResourceMenuItem(MenuItem{Title: title, Path: path, Section: section}, r.resource)
}

// guard requires an authenticated caller whose group holds key. It is the only
// middleware a guarded route needs — resolve already does what AuthMiddleware
// does.
func (a *Admin) guard(key string) inertia.HandlerFunc {
	return func(c *inertia.Context) {
		g, ok := a.resolve(c)
		if !ok {
			return
		}
		if !g.Superuser && !g.Permissions.Allows(key) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
