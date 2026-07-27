// Package components is the hardcoded component list: names, dependencies, and
// the paths each component owns.
//
// This is deliberately a switch-free slice of literals rather than a registry or
// manifest format. Extract an abstraction when a fifth component actually lands,
// not before.
package components

import (
	"fmt"
	"slices"
	"strings"
)

// Component is one optional feature of the template.
type Component struct {
	Name string
	// Deps are components that must be present whenever this one is.
	Deps []string
	// Owned are repo-relative paths deleted when the component is off. Missing
	// paths are tolerated: the template may legitimately not have them yet.
	Owned []string
}

// Tooling is the reserved pseudo-component covering the template's own
// generator and template-maintenance code. Its marker blocks are always
// stripped and its paths always deleted — no generated project keeps them. It
// is not selectable and never appears in the checklist.
const Tooling = "tooling"

// All is the component list, in checklist order.
var All = []Component{
	{
		Name:  "db",
		Owned: []string{"internal/service/db", "internal/driver"},
	},
	{
		Name:  "session",
		Owned: []string{"internal/service/session"},
	},
	{
		Name: "admin",
		Deps: []string{"session", "db"},
		Owned: []string{
			"internal/controller/admin",
			"commands/admin_user.go",
			"frontend/pages/admin",
			"frontend/src/components/admin",
			"frontend/src/components/ui",
			"frontend/src/lib",
			"server/ssr_admin_test.go",
			"server/ssr_newpages_test.go",
			// The dark-mode boot script it covers is admin-only wiring (a
			// goappctl:admin block in rootHTML); without admin the script is
			// stripped to empty and the test's assertions would fail.
			"server/roothtml_test.go",
		},
	},
	{
		Name: "storage",
		// No Deps: the service and its public route stand alone. The file
		// manager UI lives inside the admin area and will be listed here too —
		// admin's directories go wholesale, so naming its files individually is
		// what makes "admin on, storage off" strip correctly. Those entries
		// arrive with the files themselves; TestOwnedPathsExist refuses a path
		// that is not there yet.
		Owned: []string{
			"internal/service/storage",
			"server/uploads_route_test.go",
		},
	},
	{
		Name: "ssr",
		Owned: []string{
			"frontend/ssr",
			"frontend/ssr-esm-render.ts",
			"frontend/vite.config.ssr.ts",
			"server/ssr_admin_test.go",
		},
	},
}

// ToolingPaths are removed from every generated project: the generator itself,
// the template's own docs and CI, and the workspace file, which points at local
// sibling checkouts and would break any build elsewhere.
var ToolingPaths = []string{
	"cmd/goappctl",
	"docs",
	".github/workflows/goappctl.yml",
	// The committed rendering of the generated admin list page, and the test that
	// renders it through QuickJS. They exist so the template can test what
	// `gen admin` produces — a generated project has no use for either.
	"frontend/pages/admin/ssrfixture",
	"server/ssr_fixture_test.go",
	"go.work",
	"go.work.sum",
	"app.db",
	"bin",
}

// Names returns every selectable component name, in checklist order.
func Names() []string {
	out := make([]string, 0, len(All))
	for _, c := range All {
		out = append(out, c.Name)
	}
	return out
}

// Get looks up a component by name.
func Get(name string) (Component, bool) {
	for _, c := range All {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// Known reports every valid marker name, including the reserved tooling name.
func Known() map[string]bool {
	out := map[string]bool{Tooling: true}
	for _, c := range All {
		out[c.Name] = true
	}
	return out
}

// Closure validates the selection and adds every transitive dependency. It
// returns the resolved set in checklist order plus the names closure had to add,
// so the caller can tell the user what it decided on their behalf.
func Closure(selected []string) (resolved, added []string, err error) {
	want := map[string]bool{}
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == Tooling {
			return nil, nil, fmt.Errorf("%q is reserved and cannot be selected", Tooling)
		}
		if _, ok := Get(name); !ok {
			return nil, nil, fmt.Errorf("unknown component %q (known: %s)", name, strings.Join(Names(), ", "))
		}
		want[name] = true
	}

	for grew := true; grew; {
		grew = false
		for name := range want {
			c, _ := Get(name)
			for _, dep := range c.Deps {
				if !want[dep] {
					want[dep] = true
					added = append(added, dep)
					grew = true
				}
			}
		}
	}

	for _, c := range All {
		if want[c.Name] {
			resolved = append(resolved, c.Name)
		}
	}
	slices.Sort(added)
	return resolved, added, nil
}

// Off returns the components not in resolved, plus the always-stripped tooling
// name — i.e. exactly the marker names to delete.
func Off(resolved []string) map[string]bool {
	out := map[string]bool{Tooling: true}
	for _, c := range All {
		if !slices.Contains(resolved, c.Name) {
			out[c.Name] = true
		}
	}
	return out
}
