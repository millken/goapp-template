//go:build !prod

package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/millken/inertia/ssr"
	"github.com/millken/inertia/ssr/quickjs"
)

// TestSSR_GeneratedAdminListRendersUnderQuickJS is the SSR coverage for what
// `goappctl gen admin` actually produces. TestSSR_AdminDashboardRendersUnderQuickJS
// cannot provide it: the dashboard is Card + Alert + Button, static markup with no
// floating content, while every component carrying QuickJS risk — Dialog,
// DropdownMenu, Select and the TanStack pager — appears only in the generated list
// page. That page is a Go template with [[ ]] delimiters, so it never lands in
// frontend/pages/ on its own; frontend/pages/admin/ssrfixture/index.vue is its
// committed rendering, kept in step by TestAdminIndexFixtureIsCurrent.
func TestSSR_GeneratedAdminListRendersUnderQuickJS(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the QuickJS VM in -short mode")
	}
	const bundlePath = "../frontend/dist/" + ssrBundleName
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Skipf("no SSR bundle at %s; run `pnpm -C frontend build:ssr` first", bundlePath)
	}

	vm, err := quickjs.NewVM(ssr.WithDefaultCache(1), ssr.WithBundlerJS(string(bundle)))
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// More rows than one page, so the pager renders rather than being v-if'd away.
	// PAGE_SIZE in the template is 20; 95 rows gives five pages and an ellipsis.
	items := make([]map[string]any, 0, 95)
	for i := 1; i <= 95; i++ {
		items = append(items, map[string]any{"id": i, "name": fmt.Sprintf("row %d", i)})
	}

	html, err := vm.RenderComponent(ctx, "admin/ssrfixture/index", map[string]any{
		"items":      items,
		"basePath":   "/admin/ssrfixture",
		"adminMount": "/admin",
		"loginPath":  "/admin/login",
		"adminMenu":  []map[string]any{{"title": "Fixture", "path": "/admin/ssrfixture"}},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/ssrfixture/index): %v", err)
	}

	// Rows, the sortable header, and the row-action trigger must all be there —
	// a component that threw during render would take its subtree with it.
	for _, want := range []string{"row 1", "row 20", "Name", "pagination-item"} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR output missing %q", want)
		}
	}
	// Row 21 is on page two; if it rendered, pagination is not being applied.
	if strings.Contains(html, "row 21") {
		t.Error("row 21 rendered on page one — the pagination row model is not applied")
	}

	// The defect this test exists for. PaginationItem renders its own button, so
	// using it as a wrapper for Prev/Next nests a button inside a button; browsers
	// reparse that and hydration then disagrees with the server's markup. Only a
	// real render catches it — the generator's string assertions cannot.
	if d := maxButtonDepth(html); d > 1 {
		t.Errorf("nested <button> at depth %d: an element that renders its own "+
			"button is being used as a wrapper", d)
	}

	// Overlays are teleported, and frontend/ssr/render.ts does not collect
	// ctx.teleports — so a dialog that renders on the server would hydrate against
	// markup the client never received.
	if strings.Contains(html, `role="dialog"`) {
		t.Error("a dialog rendered during SSR; overlays must start closed")
	}
}
