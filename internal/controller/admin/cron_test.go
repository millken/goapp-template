package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/service/queue"
)

// cronID is the id of the plan loginStackWithStore's registry declares. Read rather than
// assumed: the row is created by the queue's startup sync, not by a fixture.
func cronID(t *testing.T, adm *Admin) int64 {
	t.Helper()
	plans, err := adm.Queue.Schedules(context.Background())
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	for _, p := range plans {
		if p.Name == queueCronKind {
			return p.ID
		}
	}
	t.Fatalf("the registered plan %q was not synced; plans = %+v", queueCronKind, plans)
	return 0
}

func cronPlan(t *testing.T, adm *Admin, id int64) queue.ScheduleView {
	t.Helper()
	p, err := adm.Queue.Schedule(context.Background(), id)
	if err != nil {
		t.Fatalf("find schedule %d: %v", id, err)
	}
	if p == nil {
		t.Fatalf("schedule %d is gone", id)
	}
	return *p
}

// --- list ---

// The startup sync is what puts the row there, so this doubles as an end-to-end check
// that a code registration reaches the screen.
func TestCronsIndex_ShowsTheRegisteredPlan(t *testing.T) {
	eng, adm, cookie := taskStack(t)

	props := pageProps(t, eng, cookie, "/admin/cron")
	items := props["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d plans, want the one the registry declares", len(items))
	}
	p := items[0].(map[string]any)
	if p["name"] != queueCronKind || p["kind"] != queueCronKind {
		t.Errorf("plan = %v", p)
	}
	if p["spec"] != "0 3 * * *" || p["code_spec"] != "0 3 * * *" {
		t.Errorf("spec/code_spec = %v / %v", p["spec"], p["code_spec"])
	}
	if p["drifted"].(bool) {
		t.Error("a freshly synced plan must not read as drifted")
	}
	if !p["enabled"].(bool) || !p["present"].(bool) || !p["known_kind"].(bool) {
		t.Errorf("plan should be live: %v", p)
	}
	if p["next_run_at_text"] == "" {
		t.Error("next_run_at_text is empty; the list has nothing to show")
	}
	_ = adm
}

// --- editing the expression ---

func TestCronUpdate_SavesAndRecomputesTheNextRun(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)
	before := cronPlan(t, adm, id)

	w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d", id),
		url.Values{"expression": {"*/15 * * * *"}})
	if w.Code != http.StatusFound {
		t.Fatalf("POST → %d, want a redirect\n%s", w.Code, w.Body.String())
	}

	after := cronPlan(t, adm, id)
	if after.Spec != "*/15 * * * *" {
		t.Errorf("spec = %q, want the submitted expression", after.Spec)
	}
	// The code's default is untouched, which is what makes the drift visible and the
	// reset possible.
	if after.CodeSpec != before.CodeSpec {
		t.Errorf("code_spec changed to %q; only the code may move it", after.CodeSpec)
	}
	if !after.Drifted {
		t.Error("an edited plan should read as drifted")
	}
	// Without recomputing, the new expression would not take effect until the old
	// next_run_at passed — up to a day for a daily plan, which is exactly when an
	// operator concludes the screen is broken.
	if after.NextRunAt == before.NextRunAt {
		t.Error("next_run_at was not recomputed; the new expression would not take effect")
	}
}

func TestCronUpdate_RejectsABadExpression(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)
	before := cronPlan(t, adm, id)

	r, _ := postForm(t, eng, cookie, fmt.Sprintf("/admin/cron/%d", id),
		url.Values{"expression": {"0 99 * * *"}})
	r.Header.Set("X-Pjax", "true")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	// Re-rendered with an error, not redirected: that is internal/validate's contract,
	// and the same shape groupUpdate uses for a name clash.
	if w.Code == http.StatusFound {
		t.Fatalf("a bad expression was accepted (redirected)")
	}
	body := w.Body.String()
	if !strings.Contains(body, "expression") || !strings.Contains(body, "无法解析") {
		t.Errorf("response carries no field error for expression:\n%s", body)
	}
	// And the submitted value comes back, so the operator can fix it rather than retype
	// it.
	if !strings.Contains(body, "0 99 * * *") {
		t.Error("the rejected expression was not sent back to the form")
	}

	if got := cronPlan(t, adm, id); got.Spec != before.Spec || got.NextRunAt != before.NextRunAt {
		t.Errorf("a rejected submission changed the row: %+v", got)
	}
}

// The form validates through the same parser the scheduler runs on. Anything else is how
// "saved fine, never fires" happens, so a few shapes the parser is strict about are
// checked here too.
func TestCronUpdate_ValidatesWithTheSchedulersParser(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	accepted := []string{"@daily", "@every 30s", "*/5 * * * *", "0 0 13 * FRI", "0 0 * * 5-7"}
	for _, expr := range accepted {
		w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d", id),
			url.Values{"expression": {expr}})
		if w.Code != http.StatusFound {
			t.Errorf("%q was rejected but the scheduler accepts it", expr)
		}
	}

	rejected := []string{"", "not a cron", "0 0 32 * *", "0 0 * * 8", "@fortnightly", "*/0 * * * *"}
	for _, expr := range rejected {
		w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d", id),
			url.Values{"expression": {expr}})
		if w.Code == http.StatusFound {
			t.Errorf("%q was accepted but the scheduler cannot use it", expr)
		}
	}
}

func TestCronEdit_PreviewsTheNextRuns(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	props := pageProps(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/edit", id))
	runs := props["nextRuns"].([]any)
	if len(runs) != 5 {
		t.Fatalf("%d previewed runs, want 5", len(runs))
	}
	for _, r := range runs {
		if r.(string) == "" {
			t.Error("a previewed run is empty")
		}
	}
	// The input's value is a separate prop from the stored spec, so a rejected
	// submission can redisplay what was typed.
	if props["expression"] != "0 3 * * *" {
		t.Errorf("expression = %v, want the stored spec on a fresh load", props["expression"])
	}
}

func TestCronEdit_404ForAMissingPlan(t *testing.T) {
	eng, _, cookie := taskStack(t)
	r := httptest.NewRequest(http.MethodGet, "/admin/cron/99999/edit", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET a missing plan → %d, want 404", w.Code)
	}
}

// --- reset ---

func TestCronReset_RestoresTheCodeDefault(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d", id),
		url.Values{"expression": {"*/15 * * * *"}})
	if !cronPlan(t, adm, id).Drifted {
		t.Fatal("the plan should be drifted after an edit")
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/reset", id), url.Values{})
	if w.Code != http.StatusFound {
		t.Fatalf("POST reset → %d, want a redirect", w.Code)
	}

	after := cronPlan(t, adm, id)
	if after.Spec != after.CodeSpec {
		t.Errorf("spec = %q, code_spec = %q; reset should have equalised them", after.Spec, after.CodeSpec)
	}
	if after.Drifted {
		t.Error("the plan still reads as drifted after a reset")
	}
}

// --- enable / pause ---

// The value comes from the request, not from negating the stored one: a form submitted
// twice must not flip it back. Same reason userSetStatus reads it from the request.
func TestCronSetEnabled_IsNotAToggle(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	for range 2 {
		post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id),
			url.Values{"enabled": {"0"}})
		if cronPlan(t, adm, id).Enabled {
			t.Fatal("enabled=0 twice left the plan enabled; the handler is toggling")
		}
	}

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id),
		url.Values{"enabled": {"1"}})
	if !cronPlan(t, adm, id).Enabled {
		t.Error("enabled=1 did not resume the plan")
	}
}

// Resuming recomputes the next firing from now. A plan paused for a week would otherwise
// come back with a next run far in the past and fire immediately, which is not what
// "unpause" means.
func TestCronSetEnabled_ResumingRecomputesTheNextRun(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	ctx := context.Background()
	id := cronID(t, adm)

	// Pause, then put its next firing in the past the way a week of downtime would.
	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id), url.Values{"enabled": {"0"}})
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE queue_schedules SET next_run_at = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id), url.Values{"enabled": {"1"}})

	if got := cronPlan(t, adm, id).NextRunAt; got <= 1 {
		t.Errorf("next_run_at = %d; resuming should have recomputed it from now", got)
	}
}

// --- manual trigger ---

func TestCronRunNow_EnqueuesOneTaskAndLeavesTheScheduleAlone(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)
	before := cronPlan(t, adm, id)

	w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/run", id), url.Values{})
	if w.Code != http.StatusFound {
		t.Fatalf("POST run → %d, want a redirect", w.Code)
	}

	n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_tasks WHERE schedule_id = ?`, id)
	if n != 1 {
		t.Errorf("%d tasks enqueued, want 1", n)
	}
	after := cronPlan(t, adm, id)
	// A manual run is an extra one, not a replacement: advancing next_run_at would
	// silently skip a scheduled cycle.
	if after.NextRunAt != before.NextRunAt {
		t.Errorf("next_run_at moved from %d to %d; a manual trigger must not skip a cycle",
			before.NextRunAt, after.NextRunAt)
	}
	if after.LastTaskID == 0 {
		t.Error("last_task_id was not recorded; the list could not show the outcome")
	}
}

// "Stop running this automatically" and "never run this again" are different requests, and
// verifying a plan by hand right after pausing it is what happens next.
func TestCronRunNow_WorksOnAPausedPlan(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id), url.Values{"enabled": {"0"}})
	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/run", id), url.Values{})

	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_tasks WHERE schedule_id = ?`, id); n != 1 {
		t.Errorf("%d tasks from a paused plan, want 1", n)
	}
}

// A plan whose kind has no handler here would enqueue a task nothing can claim, so the
// trigger is refused rather than producing one.
func TestCronRunNow_RefusesAPlanWithNoHandler(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	ctx := context.Background()

	// A hand-written row, the only way such a plan can exist: Start refuses one from the
	// code's own registrations.
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO queue_schedules
		   (name, kind, payload, spec, code_spec, enabled, present, next_run_at,
		    last_fire_at, last_task_id, max_attempts, created_at, updated_at)
		 VALUES ('orphan:plan', ?, '{}', '@daily', '@daily', 1, 1, 1, 0, 0, 3, 0, 0)`,
		queueOrphan); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM queue_schedules WHERE name = 'orphan:plan'`).Scan(&id); err != nil {
		t.Fatal(err)
	}

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/run", id), url.Values{})

	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_tasks`); n != 0 {
		t.Errorf("%d tasks enqueued; a plan with no handler must not produce one", n)
	}
}

// --- deletion ---

// Deleting a plan the code still declares achieves nothing — the next start syncs it back
// — so the route refuses it. The guard is in the queue service, not only in the UI.
func TestCronDelete_RefusesALivePlan(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/delete", id), url.Values{})

	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_schedules WHERE id = ?`, id); n != 1 {
		t.Error("a plan the code still declares was deleted")
	}
}

func TestCronDelete_AllowsAPlanTheCodeDropped(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	ctx := context.Background()
	id := cronID(t, adm)

	// What markAbsentSchedules does when the code stops declaring a plan.
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE queue_schedules SET present = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/delete", id), url.Values{})

	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_schedules WHERE id = ?`, id); n != 0 {
		t.Error("a plan the code no longer declares was not deleted")
	}
}

// --- routes that must not exist ---

// "The code is the source of truth" is implemented as two absent routes. A 404 here is the
// feature, so it is asserted rather than assumed.
func TestCronRoutes_CannotCreateAPlan(t *testing.T) {
	eng, _, cookie := taskStack(t)

	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/admin/cron/new"},
		{http.MethodPost, "/admin/cron"},
	} {
		var r *http.Request
		if probe.method == http.MethodPost {
			r, _ = postForm(t, eng, cookie, probe.path, url.Values{})
		} else {
			r = httptest.NewRequest(probe.method, probe.path, nil)
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s → %d, want 404: a plan may only come from a code registration",
				probe.method, probe.path, w.Code)
		}
	}
}

// The cron screens must not collide with the task ones. /admin/task/cron would have been
// matched by /admin/task/:id, so the plans live at their own second segment.
func TestCronRoutes_DoNotCollideWithTaskRoutes(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := cronID(t, adm)

	// The cron list is its own page, not a task detail page for a task called "cron".
	props := pageProps(t, eng, cookie, "/admin/cron")
	if _, ok := props["items"]; !ok {
		t.Error("/admin/cron did not render the plan list")
	}
	// And a task detail page still works alongside it.
	taskID := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 1)
	detail := pageProps(t, eng, cookie, fmt.Sprintf("/admin/task/%d", taskID))
	if _, ok := detail["attempts"]; !ok {
		t.Error("the task detail page stopped rendering")
	}
	_ = id
}

// --- permissions ---

func TestCronRoutes_ActionsRequireModify(t *testing.T) {
	eng, adm := loginStack(t)
	id := cronID(t, adm)
	putInGroup(t, adm, "CronReadOnly", false, `["cron.access"]`)
	cookie := loginAndGetCookie(t, eng)

	w := post(t, eng, cookie, fmt.Sprintf("/admin/cron/%d/enabled", id),
		url.Values{"enabled": {"0"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("POST with only cron.access → %d, want 403", w.Code)
	}
	if !cronPlan(t, adm, id).Enabled {
		t.Error("a 403 changed the plan anyway")
	}
}

// The cron list links a plan to the task it produced, but cron.modify does not imply
// task.access — so the link has to be conditional, and the flag it depends on comes from
// resolve rather than a second group query.
func TestCronsIndex_HidesTheTaskLinkWithoutTaskAccess(t *testing.T) {
	eng, adm := loginStack(t)
	putInGroup(t, adm, "CronOnly2", false, `["cron.access"]`)
	cookie := loginAndGetCookie(t, eng)

	props := pageProps(t, eng, cookie, "/admin/cron")
	if props["canViewTasks"] != false {
		t.Errorf("canViewTasks = %v, want false for a caller without task.access", props["canViewTasks"])
	}
	_ = adm
}
