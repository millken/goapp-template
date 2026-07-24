package admin

import "sort"

// MenuItem is a navigation entry shown in the admin shell. Generated admin
// resources register one via AddMenuItem so the dashboard/layout can link them.
type MenuItem struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Order int    `json:"order"` // ascending; ties broken by Title
}

// AddMenuItem registers a navigation entry. Contract: call during startup wiring
// (before Serve). The menu slice is only read at request time via menuItems, so
// no locking is needed under that contract.
func (a *Admin) AddMenuItem(item MenuItem) {
	a.menu = append(a.menu, item)
}

// menuItems returns a sorted copy of the registered menu, injected as a shared
// prop on authenticated admin requests.
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
