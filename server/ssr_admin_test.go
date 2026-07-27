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

	menu := []map[string]any{{"title": "Posts", "path": "/admin/posts", "section": "内容"}}

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
		"flash":       map[string]string{"error": "该账号已被禁用。"},
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
	for _, want := range []string{"仪表盘", "该账号已被禁用。", "topbaruser", "内容", "概览"} {
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
		"flash":       map[string]string{"error": "该账号已被禁用。"},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/dashboard, currentPath=/admin/posts): %v", err)
	}
	for _, want := range []string{"Posts", "内容"} {
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

// TestSSR_FileManagerRendersUnderQuickJS guards the file manager page against
// the failure mode the dashboard test describes: a component touching
// document/window at module scope kills the whole bundle. FileManager fetches
// its listing on mount, which never runs server-side — so an empty grid is the
// correct server render, and the assertion is on the chrome around it.
func TestSSR_FileManagerRendersUnderQuickJS(t *testing.T) {
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

	html, err := vm.RenderComponent(ctx, "admin/filemanager/index", map[string]any{
		"basePath":    "/admin/filemanager",
		"urlPrefix":   "/uploads",
		"adminUser":   map[string]any{"id": 1, "username": "alice"},
		"adminMount":  "/admin",
		"loginPath":   "/admin/login",
		"currentPath": "/admin/filemanager",
		"adminMenu":   []map[string]any{{"title": "文件", "path": "/admin/filemanager", "section": "内容"}},
		"csrfToken":   "tok",
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/filemanager/index): %v", err)
	}
	// "全部文件" is not asserted here even though it appears in the component
	// test's mock listing: it is data the API would return, and the component
	// only loads its listing onMounted, which never runs server-side. So the
	// server genuinely has no listing and settles into its empty state —
	// "这个目录是空的" — which is what a server render can actually promise.
	for _, want := range []string{"新建目录", "上传", "这个目录是空的"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	// The server never attempted the listing request (onMounted doesn't run
	// during SSR), so it must not claim one failed either: a "网络错误" banner
	// here would be reporting a network error for a request that was never
	// made, a lie about the server's own execution. Check both the error text
	// and, separately, that no element carries the error paragraph's class —
	// guards against the message being reworded while the banner itself
	// regresses back to rendering (e.g. via a reintroduced `immediate: true`).
	if strings.Contains(html, "网络错误") {
		t.Error("rendered page contains the client network-error message, but the " +
			"server never made a request that could have failed")
	}
	// The exact class attribute of the error <p>, not a bare "text-destructive"
	// substring match: the manage-mode 删除 button renders variant="destructive",
	// which Button maps to a class including "text-destructive-foreground" — a
	// false positive that would fail even after the fix, since that button
	// itself is expected in the chrome.
	if strings.Contains(html, `class="text-sm text-destructive"`) {
		t.Error("rendered page contains the error banner's <p class=\"text-sm text-destructive\">; " +
			"the server has no listing and never tried to fetch one, so no error should render")
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
