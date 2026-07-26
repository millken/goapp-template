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

	html, err := vm.RenderComponent(ctx, "admin/dashboard", map[string]any{
		"adminUser":  "admin",
		"adminMount": "/admin",
		"loginPath":  "/admin/login",
		"adminMenu":  []map[string]any{{"title": "Posts", "path": "/admin/posts"}},
		"flash":      map[string]string{"success": "Saved"},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/dashboard): %v", err)
	}
	for _, want := range []string{"Dashboard", "Posts", "Log out", "Saved"} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR output missing %q", want)
		}
	}

	// A <button> inside a <button> is invalid, and browsers reparse it into a
	// DOM that hydration then disagrees with. The generated list page hit this
	// by misusing a component that renders its own button; this guards the
	// shell against the same mistake.
	if d := maxButtonDepth(html); d > 1 {
		t.Errorf("nested <button> at depth %d; an element that renders its own "+
			"button was used as a wrapper", d)
	}
}

// maxButtonDepth walks markup counting button opens and closes. Deliberately
// crude: it needs to spot nesting, not parse HTML.
func maxButtonDepth(html string) int {
	depth, max := 0, 0
	for i := range len(html) {
		switch {
		case strings.HasPrefix(html[i:], "</button"):
			depth--
		case strings.HasPrefix(html[i:], "<button"):
			depth++
			if depth > max {
				max = depth
			}
		}
	}
	return max
}
