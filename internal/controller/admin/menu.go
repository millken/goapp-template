package admin

import (
	"cmp"
	"slices"
)

// MenuItem is a navigation entry. Generated admin resources register one via
// Registrar.Menu, which gates it by the resource's access key; AddMenuItem is
// the ungated alternative for entries outside the permission model.
type MenuItem struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Order int    `json:"order"` // ascending; ties broken by Title
	// Section groups items in the shell's icon rail; empty means "Content".
	Section string `json:"section"`
}

// menuEntry pairs a sidebar item with the resource whose access key gates it. An
// empty resource means "always show" — that is what AddMenuItem produces, for
// entries outside the permission model.
type menuEntry struct {
	item     MenuItem
	resource string
}

// AddMenuItem registers a navigation entry that no permission gates. Call only
// during startup wiring (before Serve); the menu is read at request time, so no
// locking is needed.
func (a *Admin) AddMenuItem(item MenuItem) {
	if item.Section == "" {
		item.Section = "Content"
	}
	a.menu = append(a.menu, menuEntry{item: item})
}

// addResourceMenuItem registers an entry gated by resource's access key. Used by
// the registrar; resources go through Registrar.Menu rather than calling this.
func (a *Admin) addResourceMenuItem(item MenuItem, resource string) {
	if item.Section == "" {
		item.Section = "Content"
	}
	a.menu = append(a.menu, menuEntry{item: item, resource: resource})
}

// menuItems returns the sorted menu with entries g may not access removed. A
// sidebar full of links that all 403 is worse than a short sidebar.
func (a *Admin) menuItems(g *group) []MenuItem {
	// make+append rather than slices.Clone: an empty menu must reach the
	// frontend as [] rather than null.
	out := make([]MenuItem, 0, len(a.menu))
	for _, e := range a.menu {
		if e.resource != "" && !g.Superuser && !g.Permissions.Allows(e.resource+verbAccess) {
			continue
		}
		out = append(out, e.item)
	}
	// Section order is computed from the unfiltered menu, not out: deriving it
	// from the filtered slice would let a section jump position for a caller who
	// cannot see its first item, making the rail reorder itself per user.
	secIdx := make(map[string]int)
	for _, e := range a.menu {
		if _, ok := secIdx[e.item.Section]; !ok {
			secIdx[e.item.Section] = len(secIdx)
		}
	}
	slices.SortStableFunc(out, func(x, y MenuItem) int {
		return cmp.Or(
			cmp.Compare(secIdx[x.Section], secIdx[y.Section]),
			cmp.Compare(x.Order, y.Order),
			cmp.Compare(x.Title, y.Title),
		)
	})
	return out
}
