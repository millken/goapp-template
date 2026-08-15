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
		//goappctl:queue
		// The queue screens carry the newest markup in this admin — <details>, <pre>,
		// a <dl>, and an anchor-based pager — and the two guards below are aimed
		// squarely at the two mistakes they invite: ServerTable's pager nesting a
		// button inside an anchor, and a ConfirmDialog that did not start closed.
		//
		// These cases are inside a marker block because a build without the queue has
		// no such pages in its SSR bundle, and RenderComponent would fail on a view
		// that does not exist.
		{"admin/task/index", with(map[string]any{
			"basePath": "/admin/task",
			"items": []map[string]any{
				{
					"id": 7, "kind": "mail:welcome", "status": "dead",
					"attempts": 5, "max_attempts": 5, "run_at": 0, "run_at_text": "",
					"created_at": 1, "created_at_text": "2026-08-14 09:31:07",
					"finished_at": 1, "finished_at_text": "2026-08-14 09:33:12",
					"last_error": "dial tcp: i/o timeout", "schedule_id": 0,
					"known_kind": true,
				},
				{
					"id": 8, "kind": "report:daily", "status": "pending",
					"attempts": 2, "max_attempts": 5, "run_at": 1, "run_at_text": "2026-08-14 10:00:00",
					"created_at": 1, "created_at_text": "2026-08-14 09:00:00",
					"finished_at": 0, "finished_at_text": "",
					"last_error": "", "schedule_id": 3, "known_kind": false,
				},
			},
			"total": 400, "page": 2, "pageSize": 25,
			"filters": map[string]string{"status": "", "kind": ""},
			// No "statuses": the page builds the filter's options from lib/task-status,
			// the same table its badges use.
			"kinds":   []string{"mail:welcome"},
			"counts":  map[string]int{"pending": 3, "dead": 1},
			"orphans": map[string]int{"report:daily": 1},
			// currentPath overridden so the shell's longest-prefix match lights the
			// task entry, which also exercises startsWith(path + "/").
			"currentPath": "/admin/task",
		}), "已失败（重试用尽）"}, // only reachable through taskStatusMeta
		{"admin/task/show", with(map[string]any{
			"basePath": "/admin/task",
			"item": map[string]any{
				"id": 7, "kind": "mail:welcome", "status": "dead",
				"attempts": 2, "max_attempts": 2, "priority": 0,
				"run_at": 1, "run_at_text": "2026-08-14 09:31:00",
				"created_at": 1, "created_at_text": "2026-08-14 09:30:00",
				"started_at": 1, "started_at_text": "2026-08-14 09:31:01",
				"finished_at": 1, "finished_at_text": "2026-08-14 09:33:12",
				"lease_until": 0, "lease_until_text": "", "worker": "",
				"unique_key": "", "payload": `{"to":"a@b.c"}`, "timeout_ms": 0,
				"full_error":  "panic: runtime error: index out of range [3]",
				"schedule_id": 0, "known_kind": false,
			},
			"attempts": []map[string]any{
				{
					"attempt": 2, "outcome": "failed", "worker": "host/1/ab",
					"error":      "panic: runtime error: index out of range [3]\n\tmain.go:41 +0x1f",
					"started_at": 1, "started_at_text": "2026-08-14 09:33:00",
					"finished_at": 1, "finished_at_text": "2026-08-14 09:33:12", "duration_ms": 12000,
				},
			},
			"attemptTotal": 2, "attemptCap": 50,
			"currentPath": "/admin/task/7",
		}), "index out of range"}, // the <details><pre> really rendered the full stack
		{"admin/cron/index", with(map[string]any{
			"basePath": "/admin/cron", "taskBasePath": "/admin/task", "canViewTasks": true,
			"items": []map[string]any{
				{
					"id": 1, "name": "report:nightly", "kind": "report:daily",
					"spec": "*/30 * * * *", "code_spec": "0 3 * * *",
					"enabled": true, "present": true, "drifted": true, "known_kind": true,
					"next_run_at": 1, "next_run_at_text": "2026-08-14 10:30:00",
					"last_fire_at": 1, "last_fire_at_text": "2026-08-14 10:00:00",
					"last_task_id": 8, "last_status": "succeeded",
				},
				{
					"id": 2, "name": "gone:plan", "kind": "gone:kind",
					"spec": "@daily", "code_spec": "@daily",
					"enabled": false, "present": false, "drifted": false, "known_kind": false,
					"next_run_at": 0, "next_run_at_text": "",
					"last_fire_at": 0, "last_fire_at_text": "",
					"last_task_id": 0, "last_status": "",
				},
			},
			"currentPath": "/admin/cron",
		}), "代码中已移除"}, // only reachable through scheduleStatusMeta's ranking
		{"admin/cron/form", with(map[string]any{
			"basePath": "/admin/cron",
			"item": map[string]any{
				"id": 1, "name": "report:nightly", "kind": "report:daily",
				"spec": "0 3 * * *", "code_spec": "0 3 * * *",
				"enabled": true, "present": true, "drifted": false, "known_kind": true,
				"next_run_at_text": "2026-08-15 03:00:00", "last_fire_at_text": "",
			},
			"expression":  "0 99 * * *",
			"nextRuns":    []string{},
			"errors":      map[string]string{"expression": "表达式无法解析：hour: 99 is out of range 0-23"},
			"currentPath": "/admin/cron/1/edit",
		}), "99 is out of range"}, // the field error reached the form
		//goappctl:end
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
