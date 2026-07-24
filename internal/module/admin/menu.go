package admin

import "sort"

// MenuItem is a navigation entry shown in the admin shell. Generated admin
// resources register one via AddMenuItem so the dashboard/layout can link them.
type MenuItem struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Order int    `json:"order"` // ascending; ties broken by Title
}

// AddMenuItem registers a navigation entry. Contract: call during Register
// (which runs at app.Use time, before Serve). The menu slice is only read at
// request time via menuItems, so no locking is needed under that contract.
func (m *Module) AddMenuItem(item MenuItem) {
	m.menu = append(m.menu, item)
}

// menuItems returns a sorted copy of the registered menu, injected as a shared
// prop on authenticated admin requests.
func (m *Module) menuItems() []MenuItem {
	out := make([]MenuItem, len(m.menu))
	copy(out, m.menu)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Title < out[j].Title
	})
	return out
}
