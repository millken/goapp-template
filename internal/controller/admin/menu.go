package admin

import "sort"

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
	out := make([]MenuItem, len(a.menu))
	copy(out, a.menu)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Title < out[j].Title
	})
	return out
}
