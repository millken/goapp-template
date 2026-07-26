//go:build !prod

package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/millken/inertia/ssr"
	"github.com/millken/inertia/ssr/quickjs"
)

// TestSSR_AdminDashboardRendersUnderQuickJS guards the SSR path for the shadcn
// component set. QuickJS has no document/window/Intl/ResizeObserver, and a
// component that touches them at module scope kills the whole bundle: the
// exports never get assigned and every page fails with a bare
// "TypeError: not a function". The RenderTemplate probe below distinguishes that
// from a component that merely throws while rendering.
func TestSSR_AdminDashboardRendersUnderQuickJS(t *testing.T) {
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

	if _, err := vm.RenderTemplate(ctx, `<div>probe</div>`, map[string]any{}); err != nil {
		t.Fatalf("RenderTemplate probe failed, so the bundle's top-level init threw "+
			"and no page can render: %v", err)
	}

	menu := []map[string]any{{"title": "Posts", "path": "/admin/posts", "section": "Content"}}

	// AdminShell shows only the *active* section's items in the panel — that's
	// the point of the two-column design, one full column per section instead
	// of a nested tree. So a single render can prove either "Home is active"
	// (Overview visible) or "Content is active" (Posts visible), never both;
	// two renders, one per currentPath, cover both without weakening either.
	html, err := vm.RenderComponent(ctx, "admin/dashboard", map[string]any{
		"adminUser":   map[string]any{"id": 1, "username": "topbaruser"},
		"adminMount":  "/admin",
		"loginPath":   "/admin/login",
		"currentPath": "/admin",
		"adminMenu":   menu,
		"flash":       map[string]string{"success": "Saved"},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/dashboard): %v", err)
	}
	// "Log out" is deliberately not asserted here: it lives inside
	// DropdownMenuContent, which reka-ui gates behind <Presence
	// :present="open">. The dropdown starts closed, so its slot content never
	// reaches server-rendered markup at all — the same reason
	// ssr_fixture_test.go never asserts text from its own row-action dropdown.
	// The logout form's real behavior (that clicking the menu item still
	// submits, since reka-ui only intercepts a synthetic "select" event, not
	// the underlying DOM click) is a hydrated-client concern verified
	// separately, not something an SSR string match can observe.
	// "topbaruser" rather than "admin": the shell renders /admin/logout in two
	// attributes, so asserting "admin" passed whether or not the username ever
	// reached the topbar — and that hop, page prop → shell prop, is otherwise
	// untested. A name that cannot appear in a path makes the check real.
	for _, want := range []string{"Dashboard", "Saved", "topbaruser", "Content", "Overview"} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR output (currentPath=/admin) missing %q", want)
		}
	}
	if strings.Contains(html, "Posts") {
		t.Error("SSR output (currentPath=/admin) unexpectedly shows Posts; Home should be the active section")
	}

	htmlContent, err := vm.RenderComponent(ctx, "admin/dashboard", map[string]any{
		"adminUser":   map[string]any{"id": 1, "username": "topbaruser"},
		"adminMount":  "/admin",
		"loginPath":   "/admin/login",
		"currentPath": "/admin/posts",
		"adminMenu":   menu,
		"flash":       map[string]string{"success": "Saved"},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/dashboard, currentPath=/admin/posts): %v", err)
	}
	for _, want := range []string{"Posts", "Content"} {
		if !strings.Contains(htmlContent, want) {
			t.Errorf("SSR output (currentPath=/admin/posts) missing %q", want)
		}
	}

	// A <button> inside a <button> is invalid, and browsers reparse it into a
	// DOM that hydration then disagrees with. The generated list page hit this
	// by misusing a component that renders its own button; this guards the
	// shell against the same mistake, on both renders.
	for _, h := range []string{html, htmlContent} {
		if d := maxButtonDepth(h); d > 1 {
			t.Errorf("nested <button> at depth %d; an element that renders its own "+
				"button was used as a wrapper", d)
		}
	}
}

// maxButtonDepth walks markup counting button opens and closes. Deliberately
// crude: it needs to spot nesting, not parse HTML.
func maxButtonDepth(html string) int {
	depth, max := 0, 0
	for i := range len(html) {
		switch {
		case strings.HasPrefix(html[i:], "</button"):
			if depth > 0 {
				depth--
			}
		case strings.HasPrefix(html[i:], "<button"):
			depth++
			if depth > max {
				max = depth
			}
		}
	}
	return max
}
