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

// TestSSR_AdminPagesRenderUnderQuickJS renders every hand-written admin page.
// The other two SSR tests cover the dashboard and the generated-list fixture, so
// the user, group, account and login pages had no coverage of the one thing that
// matters most about a page: whether it renders at all. A handler returning the
// right props proves nothing if the page throws on them — the server still
// answers 200, and only a browser would have noticed.
func TestSSR_AdminPagesRenderUnderQuickJS(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// What resolve injects on every authenticated admin request.
	shared := map[string]any{
		"adminUser":   map[string]any{"id": 1, "username": "admin"},
		"adminMount":  "/admin",
		"loginPath":   "/admin/login",
		"currentPath": "/admin/user",
		"adminMenu": []map[string]any{
			{"title": "Users", "path": "/admin/user", "section": "Access"},
			{"title": "Groups", "path": "/admin/group", "section": "Access"},
		},
		"flash": map[string]string{"success": "Saved"},
	}
	with := func(extra map[string]any) map[string]any {
		m := make(map[string]any, len(shared)+len(extra))
		for k, v := range shared {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	for _, c := range []struct {
		view string
		// props are what the matching handler actually sets — see user_crud.go,
		// group_crud.go and account.go.
		props map[string]any
		// want is a string only reachable if the interesting branch rendered,
		// rather than something the shell would emit anyway.
		want string
	}{
		{"admin/user/index", with(map[string]any{
			"basePath": "/admin/user",
			"items": []map[string]any{
				{"id": 1, "username": "admin", "group_id": 1, "group": "Administrators", "status": 1, "created_at": 0},
				{"id": 2, "username": "erin", "group_id": 2, "group": "Editors", "status": 0, "created_at": 0},
			},
		}), "erin"},
		{"admin/user/form", with(map[string]any{
			"basePath": "/admin/user",
			"item":     map[string]any{"id": 2, "username": "erin", "group_id": 2, "group": "Editors", "status": 1},
			"groups":   []map[string]any{{"id": 1, "name": "Administrators"}, {"id": 2, "name": "Editors"}},
			"errors":   map[string]string{"password": "太长了"},
		}), "太长了"},
		{"admin/group/index", with(map[string]any{
			"basePath": "/admin/group",
			"items": []map[string]any{
				{"id": 1, "name": "Administrators", "superuser": true, "members": 1, "keys": 0},
				{"id": 2, "name": "Editors", "superuser": false, "members": 3, "keys": 2},
			},
		}), "Editors"},
		{"admin/group/form", with(map[string]any{
			"basePath":    "/admin/group",
			"item":        map[string]any{"id": 2, "name": "Editors", "superuser": false, "members": 3, "keys": 2},
			"permissions": []map[string]any{{"resource": "user", "access": true, "modify": false}},
			"stale":       []string{"gone.modify"},
		}), "gone.modify"},
		{"admin/account/password", with(map[string]any{
			"basePath": "/admin/account/password",
			"errors":   map[string]string{"current": "当前密码不正确"},
		}), "当前密码不正确"},
		// The login page is the one admin page outside AdminShell, and the only
		// place a bounced user reads why. It renders no shell props.
		{"admin/login", map[string]any{
			"loginPath": "/admin/login",
			"flash":     map[string]string{"error": "该账号已被禁用。"},
		}, "该账号已被禁用。"},
	} {
		t.Run(c.view, func(t *testing.T) {
			html, err := vm.RenderComponent(ctx, c.view, c.props)
			if err != nil {
				t.Fatalf("RenderComponent(%s): %v", c.view, err)
			}
			if !strings.Contains(html, c.want) {
				t.Errorf("rendered output missing %q", c.want)
			}
			// The same two guards the other SSR tests apply: a button inside a
			// button is reparsed by browsers and hydration then disagrees, and a
			// dialog rendered server-side hydrates against markup the client
			// never received, since teleported content is not collected.
			if d := maxButtonDepth(html); d > 1 {
				t.Errorf("nested <button> at depth %d", d)
			}
			if strings.Contains(html, `role="dialog"`) {
				t.Error("a dialog rendered during SSR; overlays must start closed")
			}
		})
	}
}
