package admin

import (
	"net/http"
	"slices"
	"strings"
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
