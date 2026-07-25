package admin

import (
	"cmp"
	"slices"
)

// MenuItem is a navigation entry; generated admin resources register one via
// AddMenuItem.
type MenuItem struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Order int    `json:"order"` // ascending; ties broken by Title
}

// AddMenuItem registers a navigation entry. Call only during startup wiring
// (before Serve); the menu is read at request time, so no locking is needed.
func (a *Admin) AddMenuItem(item MenuItem) {
	a.menu = append(a.menu, item)
}

// menuItems returns a sorted copy of the menu, injected as a shared prop.
func (a *Admin) menuItems() []MenuItem {
	// make+copy rather than slices.Clone: Clone preserves nil, and an empty menu
	// must reach the frontend as [] rather than null.
	out := make([]MenuItem, len(a.menu))
	copy(out, a.menu)
	slices.SortStableFunc(out, func(x, y MenuItem) int {
		return cmp.Or(cmp.Compare(x.Order, y.Order), cmp.Compare(x.Title, y.Title))
	})
	return out
}
