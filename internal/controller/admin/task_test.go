package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/service/queue"
	"github.com/millken/inertia"
)

// The kinds loginStackWithStore registers. queueTestKind has a handler; anything not
// in this file is an orphan, which several screens have to show differently.
const (
	queueTestKind = "test:job"
	queueCronKind = "test:nightly"
	queueOrphan   = "test:gone"
)

// taskStack is loginStack plus a logged-in cookie, in the shape fmStack uses. It does
// not re-Mount: loginStack already did, and mounting twice would fail route
// registration.
func taskStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	return eng, adm, loginAndGetCookie(t, eng)
}

// pageProps drives a GET and returns the props the handler set.
//
// The X-Pjax header is the seam: with it, Context.Render answers the props as JSON
// instead of an HTML page, so an assertion can be structural. The alternative is
// matching escaped substrings in a rendered page, which filemanager_test.go does and
// its own comment calls a compromise.
func pageProps(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(cookie)
	r.Header.Set("X-Pjax", "true")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s → %d, want 200\n%s", path, w.Code, w.Body.String())
	}
	var props map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &props); err != nil {
		t.Fatalf("GET %s did not answer JSON props: %v\n%s", path, err, w.Body.String())
	}
	return props
}

// seedQueueTask inserts a task row directly, so a test can construct states only a
// crash or a race would otherwise produce.
func seedQueueTask(t *testing.T, adm *Admin, kind, status string, attempts, maxAttempts int) int64 {
	t.Helper()
	now := time.Now().UnixNano()
	finished := int64(0)
	if status == queue.StatusSucceeded || status == queue.StatusDead || status == queue.StatusCancelled {
		finished = now
	}
	leaseToken, worker, leaseUntil := "", "", int64(0)
	if status == queue.StatusRunning {
		leaseToken, worker, leaseUntil = "test-token", "test-worker", now+int64(time.Minute)
	}

	res, err := adm.DB.ExecContext(context.Background(),
		`INSERT INTO queue_tasks
		   (kind, payload, status, priority, run_at, attempts, max_attempts, timeout_ms,
		    lease_until, lease_token, worker, schedule_id, unique_key, last_error,
		    created_at, updated_at, started_at, finished_at)
		 VALUES (?, '{}', ?, 0, ?, ?, ?, 0, ?, ?, ?, NULL, NULL, '', ?, ?, 0, ?)`,
		kind, status, now, attempts, maxAttempts, leaseUntil, leaseToken, worker,
		now, now, finished)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed task id: %v", err)
	}
	return id
}

func taskStatus(t *testing.T, adm *Admin, id int64) string {
	t.Helper()
	var status string
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT status FROM queue_tasks WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return status
}

// --- list ---

func TestTasksIndex_FiltersByStatusAndKind(t *testing.T) {
	eng, adm, cookie := taskStack(t)

	pending := seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)
	dead := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 3, 3)
	seedQueueTask(t, adm, queueOrphan, queue.StatusDead, 1, 3)

	all := pageProps(t, eng, cookie, "/admin/task")
	if got := int(all["total"].(float64)); got != 3 {
		t.Errorf("unfiltered total = %d, want 3", got)
	}

	byStatus := pageProps(t, eng, cookie, "/admin/task?status=dead")
	if got := int(byStatus["total"].(float64)); got != 2 {
		t.Errorf("status=dead total = %d, want 2", got)
	}

	// Total has to move with the filter, not just the rows: a pager built from a
	// total that ignores the filter offers pages that do not exist.
	byKind := pageProps(t, eng, cookie, "/admin/task?status=dead&kind="+queueTestKind)
	if got := int(byKind["total"].(float64)); got != 1 {
		t.Errorf("status=dead&kind total = %d, want 1", got)
	}
	items := byKind["items"].([]any)
	if len(items) != 1 || int64(items[0].(map[string]any)["id"].(float64)) != dead {
		t.Errorf("items = %v, want just task %d", items, dead)
	}
	_ = pending
}

// A filter value this package could never have produced is ignored, not honoured. It
// can only come from a hand-edited URL, and returning nothing would read as an empty
// queue — the same "do not trust a value outside the catalogue" rule submittedKeys
// applies to permission keys.
func TestTasksIndex_IgnoresUnknownFilterValues(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)

	for _, path := range []string{
		"/admin/task?status=exploded",
		"/admin/task?kind=NOT_A_KIND", // fails the shape rule, so it is not a filter
	} {
		props := pageProps(t, eng, cookie, path)
		if got := int(props["total"].(float64)); got != 1 {
			t.Errorf("GET %s total = %d, want 1 (the filter should be ignored)", path, got)
		}
		// And the page must not claim to be filtered by a value it dropped, or the
		// operator reads an unfiltered list as a filtered one.
		filters := props["filters"].(map[string]any)
		if filters["status"] != "" || filters["kind"] != "" {
			t.Errorf("GET %s echoed filters %v, want them empty", path, filters)
		}
	}
}

// A kind with no handler in this process is NOT an unknown value — it is the exact
// thing the orphan banner tells the operator to go and look at. Filtering on it used
// to be dropped, which silently turned "show me this kind" into "show me everything".
func TestTasksIndex_FiltersOnAKindWithNoHandler(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)
	seedQueueTask(t, adm, "example:removed", queue.StatusPending, 0, 3)

	props := pageProps(t, eng, cookie, "/admin/task?kind=example:removed")
	if got := int(props["total"].(float64)); got != 1 {
		t.Errorf("total = %d, want 1 — the orphan kind is a legitimate filter", got)
	}
	if got := props["filters"].(map[string]any)["kind"]; got != "example:removed" {
		t.Errorf("filters.kind = %v, want the kind that was applied", got)
	}
}

func TestTasksIndex_PagesServerSide(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	pageSize := adm.Queue.AdminPageSize()
	for range pageSize + 3 {
		seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)
	}

	first := pageProps(t, eng, cookie, "/admin/task")
	if got := len(first["items"].([]any)); got != pageSize {
		t.Errorf("page 1 has %d rows, want %d", got, pageSize)
	}
	if got := int(first["total"].(float64)); got != pageSize+3 {
		t.Errorf("total = %d, want %d — it must count the table, not the page", got, pageSize+3)
	}

	second := pageProps(t, eng, cookie, "/admin/task?page=2")
	if got := len(second["items"].([]any)); got != 3 {
		t.Errorf("page 2 has %d rows, want 3", got)
	}

	// A bookmarked page beyond the end shows the last page, not an empty one: the list
	// shrinks as tasks are pruned, and an empty page is indistinguishable from an empty
	// queue.
	far := pageProps(t, eng, cookie, "/admin/task?page=99")
	if got := int(far["page"].(float64)); got != 2 {
		t.Errorf("page=99 clamped to %d, want 2", got)
	}
	if len(far["items"].([]any)) == 0 {
		t.Error("page=99 returned no rows; it should have clamped to the last page")
	}
}

// The list has to say that a task's kind has no handler here. Such a task is never
// claimed, so without saying so it just sits there for no stated reason.
func TestTasksIndex_FlagsTasksWithNoHandler(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)
	seedQueueTask(t, adm, queueOrphan, queue.StatusPending, 0, 3)

	props := pageProps(t, eng, cookie, "/admin/task")

	known := map[string]bool{}
	for _, raw := range props["items"].([]any) {
		it := raw.(map[string]any)
		known[it["kind"].(string)] = it["known_kind"].(bool)
	}
	if !known[queueTestKind] {
		t.Errorf("%s has a handler but known_kind is false", queueTestKind)
	}
	if known[queueOrphan] {
		t.Errorf("%s has no handler but known_kind is true", queueOrphan)
	}

	orphans := props["orphans"].(map[string]any)
	if got, ok := orphans[queueOrphan]; !ok || int(got.(float64)) != 1 {
		t.Errorf("orphans = %v, want %s:1", orphans, queueOrphan)
	}
}

// Timestamps are rendered in Go. The SSR runtime is QuickJS, which has no Intl, and
// client-side formatting would read a different timezone than the server's — so the
// same prop would render two different strings and hydration would disagree.
func TestTasksIndex_FormatsTimestampsServerSide(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)

	it := pageProps(t, eng, cookie, "/admin/task")["items"].([]any)[0].(map[string]any)

	text, ok := it["created_at_text"].(string)
	if !ok || text == "" {
		t.Fatalf("created_at_text = %v, want a rendered timestamp", it["created_at_text"])
	}
	if _, err := time.Parse("2006-01-02 15:04:05", text); err != nil {
		t.Errorf("created_at_text %q is not the expected format: %v", text, err)
	}
	// The raw stamp rides along so the page can test for "not set".
	if _, ok := it["created_at"].(float64); !ok {
		t.Error("the raw created_at should be sent as well")
	}
	// A never-finished task renders empty, not 1970.
	if got := it["finished_at_text"]; got != "" {
		t.Errorf("finished_at_text = %v for an unfinished task, want empty", got)
	}
}

func TestTasksIndex_CountsEveryStatus(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)
	seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 3, 3)

	counts := pageProps(t, eng, cookie, "/admin/task")["counts"].(map[string]any)
	if int(counts["pending"].(float64)) != 1 || int(counts["dead"].(float64)) != 1 {
		t.Errorf("counts = %v", counts)
	}
	// Zeroes are present so the summary row does not change shape as the queue drains.
	if _, ok := counts["running"]; !ok {
		t.Error("a status with no tasks should still be present with a zero")
	}
}

// Jump-to-id instead of a free-text search: the only text worth searching is
// last_error, which is an unindexed LIKE over the largest table in the schema.
func TestTasksIndex_GotoJumpsToATask(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)

	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/task?goto=%d", id), nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("goto → %d, want a redirect", w.Code)
	}
	if got, want := w.Header().Get("Location"), fmt.Sprintf("/admin/task/%d", id); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// A miss flashes and falls through rather than 404ing: the id came from a text box, so a
// typo is the likely cause and an error page is a dead end.
func TestTasksIndex_GotoAMissingIDFallsBackToTheList(t *testing.T) {
	eng, _, cookie := taskStack(t)

	for _, q := range []string{"goto=99999", "goto=abc", "goto=0"} {
		r := httptest.NewRequest(http.MethodGet, "/admin/task?"+q, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, r)

		if w.Code != http.StatusFound {
			t.Errorf("%s → %d, want a redirect back to the list", q, w.Code)
			continue
		}
		if got := w.Header().Get("Location"); got != "/admin/task" {
			t.Errorf("%s → Location %q, want /admin/task", q, got)
		}
	}
}

// --- detail ---

func TestTaskShow_RendersTheFailureLog(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 2, 2)
	seedAttempt(t, adm.DB, id, 1, "failed", "dial tcp: i/o timeout")
	seedAttempt(t, adm.DB, id, 2, "failed", "panic: runtime error\n\tmain.go:41 +0x1f")

	props := pageProps(t, eng, cookie, fmt.Sprintf("/admin/task/%d", id))

	attempts := props["attempts"].([]any)
	if len(attempts) != 2 {
		t.Fatalf("%d attempts, want 2", len(attempts))
	}
	// Newest first: the question is almost always "why did it stop".
	first := attempts[0].(map[string]any)
	if int(first["attempt"].(float64)) != 2 {
		t.Errorf("first row is attempt %v, want the newest (2)", first["attempt"])
	}
	// The full multi-line error survives — the detail page is where a stack is read.
	if !strings.Contains(first["error"].(string), "main.go:41") {
		t.Errorf("attempt error was truncated: %q", first["error"])
	}
	if _, ok := first["duration_ms"].(float64); !ok {
		t.Error("duration_ms should be computed for the log")
	}
	if got := int(props["attemptTotal"].(float64)); got != 2 {
		t.Errorf("attemptTotal = %d, want 2", got)
	}
}

func TestTaskShow_404ForAMissingTask(t *testing.T) {
	eng, _, cookie := taskStack(t)

	r := httptest.NewRequest(http.MethodGet, "/admin/task/99999", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	// A missing row is a 404 and a database failure is a 500 — the (nil, nil) contract
	// findGroupRow established.
	if w.Code != http.StatusNotFound {
		t.Errorf("GET a missing task → %d, want 404", w.Code)
	}
}

// --- actions ---

// Every action's precondition is a WHERE clause, so the matrix of allowed states is the
// contract. Table-driven over all five statuses, because a hole in it is a state where an
// action either silently does nothing or does something surprising.
func TestTaskActions_StatusMatrix(t *testing.T) {
	cases := []struct {
		action string
		allow  []string
	}{
		{"retry", []string{queue.StatusSucceeded, queue.StatusDead, queue.StatusCancelled}},
		{"run", []string{queue.StatusPending, queue.StatusSucceeded, queue.StatusDead, queue.StatusCancelled}},
		{"cancel", []string{queue.StatusPending, queue.StatusRunning}},
		{"delete", []string{queue.StatusPending, queue.StatusSucceeded, queue.StatusDead, queue.StatusCancelled}},
	}
	all := []string{
		queue.StatusPending, queue.StatusRunning, queue.StatusSucceeded,
		queue.StatusDead, queue.StatusCancelled,
	}

	for _, c := range cases {
		for _, status := range all {
			allowed := false
			for _, s := range c.allow {
				if s == status {
					allowed = true
				}
			}
			t.Run(c.action+"/"+status, func(t *testing.T) {
				eng, adm, cookie := taskStack(t)
				id := seedQueueTask(t, adm, queueTestKind, status, 1, 3)

				// A whole-row snapshot, not just the status. "run" on a pending task
				// leaves the status alone and moves run_at, so a status-only comparison
				// would let that case pass without the action having done anything.
				before := queueRowSnapshot(t, adm.DB, id)

				w := post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/%s", id, c.action), nil)
				if w.Code != http.StatusFound {
					t.Fatalf("POST → %d, want a redirect either way", w.Code)
				}

				gone := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, id) == 0
				if c.action == "delete" {
					if allowed != gone {
						t.Errorf("delete from %s: gone=%v, want %v", status, gone, allowed)
					}
					return
				}
				if gone {
					t.Fatalf("%s deleted the task", c.action)
				}

				after := queueRowSnapshot(t, adm.DB, id)
				changed := before != after
				if allowed && !changed {
					t.Errorf("%s from %s did nothing: row is still %+v", c.action, status, after)
				}
				// The refusal direction is the one that matters most: the guard is a
				// WHERE clause, so a refused action must leave the row byte-identical
				// rather than partially applied.
				if !allowed && changed {
					t.Errorf("%s from %s changed the row: %+v → %+v", c.action, status, before, after)
				}
			})
		}
	}
}

// Retry raises the ceiling and leaves attempts alone. Zeroing attempts would make
// queue_attempts show attempt 1 twice, forcing the detail page to number rows by
// position rather than by the value it stores.
func TestTaskRetry_RaisesTheCeilingWithoutZeroingAttempts(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 3, 3)

	post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/retry", id), nil)

	var status string
	var attempts, maxAttempts int
	var lastErr string
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT status, attempts, max_attempts, last_error FROM queue_tasks WHERE id = ?`, id).
		Scan(&status, &attempts, &maxAttempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != queue.StatusPending {
		t.Errorf("status = %q, want pending", status)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 unchanged — it is the audit trail", attempts)
	}
	if maxAttempts != 4 {
		t.Errorf("max_attempts = %d, want 4 (attempts + 1)", maxAttempts)
	}
	if lastErr != "" {
		t.Errorf("last_error = %q, want it cleared", lastErr)
	}
}

// "Run now" must not execute anything inline. Running it in the request would hold the
// HTTP connection for the handler's whole duration, run it in the web process, and
// bypass the lease so a polling worker could run it too.
func TestTaskRunNow_DoesNotExecuteInline(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusPending, 0, 3)

	post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/run", id), nil)

	if got := taskStatus(t, adm, id); got != queue.StatusPending {
		t.Errorf("status = %q, want pending: the worker runs it, not the request", got)
	}
	// The clinching assertion: an execution would have written an attempt row.
	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_attempts WHERE task_id = ?`, id); n != 0 {
		t.Errorf("%d attempt rows, want 0 — the task must not have run in the request", n)
	}
}

func TestTaskDelete_RemovesTheFailureLogToo(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 1)
	seedAttempt(t, adm.DB, id, 1, "failed", "boom")

	post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/delete", id), nil)

	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_tasks WHERE id = ?`, id); n != 0 {
		t.Error("the task survived")
	}
	if n := countQueueRows(t, adm.DB, `SELECT COUNT(*) FROM queue_attempts WHERE task_id = ?`, id); n != 0 {
		t.Error("its attempts survived")
	}
}

// An action taken from page 3 of a filtered list returns there, rather than dumping the
// operator at the top.
func TestTaskActions_PreserveTheListFilters(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 3)

	w := post(t, eng, cookie,
		fmt.Sprintf("/admin/task/%d/retry?status=dead&kind=%s&page=2", id, queueTestKind), nil)
	loc := w.Header().Get("Location")
	for _, want := range []string{"status=dead", "kind=" + url.QueryEscape(queueTestKind), "page=2"} {
		if !strings.Contains(loc, want) {
			t.Errorf("Location %q should carry %q", loc, want)
		}
	}

	// back=detail returns to the task instead.
	w = post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/retry?back=detail", id), nil)
	if got, want := w.Header().Get("Location"), fmt.Sprintf("/admin/task/%d", id); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// The redirect target is rebuilt from keys the handler recognises, never from a
// submitted location. Nothing an attacker puts in the query string can reach the
// Location header.
func TestTaskActions_RedirectTargetCannotBeInjected(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 3)

	hostile := url.Values{
		"status": {"https://evil.example/"},
		"kind":   {"//evil.example/x"},
		"page":   {"1<script>"},
	}
	w := post(t, eng, cookie,
		fmt.Sprintf("/admin/task/%d/retry?%s", id, hostile.Encode()), nil)

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/task") {
		t.Fatalf("Location = %q, want it to stay inside the admin area", loc)
	}
	for _, bad := range []string{"evil.example", "script"} {
		if strings.Contains(loc, bad) {
			t.Errorf("Location %q leaked %q from the query string", loc, bad)
		}
	}
}

// Deletion of a task also has to leave the detail page it might have returned to, which
// no longer exists.
func TestTaskDelete_AlwaysReturnsToTheList(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 3)

	w := post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/delete?back=detail", id), nil)
	if got := w.Header().Get("Location"); strings.Contains(got, fmt.Sprintf("/task/%d", id)) {
		t.Errorf("Location = %q, want the list: the detail page of a deleted task 404s", got)
	}
}

// --- permissions ---

// Two resources means two independent boundaries, which is the whole reason for
// splitting them. A group holding only cron.modify must not be able to read the task
// list.
func TestQueueRoutes_TaskAndCronArePermissionedSeparately(t *testing.T) {
	eng, adm := loginStack(t)
	putInGroup(t, adm, "CronOnly", false, `["cron.access","cron.modify"]`)
	cookie := loginAndGetCookie(t, eng)

	r := httptest.NewRequest(http.MethodGet, "/admin/task", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/task with only cron permissions → %d, want 403", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/admin/cron", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Error("GET /admin/cron with cron.access → 403")
	}
}

// Reading is not writing: task.access alone must not permit an action.
func TestQueueRoutes_ActionsRequireModify(t *testing.T) {
	eng, adm := loginStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 3)
	putInGroup(t, adm, "ReadOnly", false, `["task.access"]`)
	cookie := loginAndGetCookie(t, eng)

	w := post(t, eng, cookie, fmt.Sprintf("/admin/task/%d/retry", id), nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST retry with only task.access → %d, want 403", w.Code)
	}
	if got := taskStatus(t, adm, id); got != queue.StatusDead {
		t.Errorf("status = %q; a 403 must not have changed anything", got)
	}
}

// Every unsafe method is CSRF-checked by the session middleware. Asserted here too
// because these routes change state and a regression in the wiring would be silent.
func TestQueueActions_RequireTheCSRFToken(t *testing.T) {
	eng, adm, cookie := taskStack(t)
	id := seedQueueTask(t, adm, queueTestKind, queue.StatusDead, 1, 3)

	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/admin/task/%d/retry", id), nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("POST without a token → %d, want 403", w.Code)
	}
	if got := taskStatus(t, adm, id); got != queue.StatusDead {
		t.Errorf("status = %q; a rejected request must not have changed anything", got)
	}
}

// There is no route that creates a task, and its absence is the enforcement of "a task's
// kind must have a handler registered in code". A 404 here is the feature.
func TestTaskRoutes_HaveNoCreateRoute(t *testing.T) {
	eng, _, cookie := taskStack(t)

	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/admin/task/new"},
		{http.MethodPost, "/admin/task"},
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
			t.Errorf("%s %s → %d, want 404: the admin area must not be able to create a task",
				probe.method, probe.path, w.Code)
		}
	}
}

// --- helpers ---

func seedAttempt(t *testing.T, db *sqldb.DB, taskID int64, attempt int, outcome, msg string) {
	t.Helper()
	now := time.Now().UnixNano()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO queue_attempts
		   (task_id, attempt, outcome, worker, error, started_at, finished_at)
		 VALUES (?, ?, ?, 'test-worker', ?, ?, ?)`,
		taskID, attempt, outcome, msg, now, now+int64(time.Second)); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
}

// queueRowState is the part of a task row an action can touch, comparable so a test can
// assert "changed" or "byte-identical" without listing fields at every call site.
type queueRowState struct {
	Status      string
	RunAt       int64
	Attempts    int
	MaxAttempts int
	LeaseToken  string
	LastError   string
	FinishedAt  int64
}

func queueRowSnapshot(t *testing.T, db *sqldb.DB, id int64) queueRowState {
	t.Helper()
	var s queueRowState
	if err := db.QueryRowContext(context.Background(),
		`SELECT status, run_at, attempts, max_attempts, lease_token, last_error, finished_at
		   FROM queue_tasks WHERE id = ?`, id).
		Scan(&s.Status, &s.RunAt, &s.Attempts, &s.MaxAttempts, &s.LeaseToken,
			&s.LastError, &s.FinishedAt); err != nil {
		t.Fatalf("snapshot task %d: %v", id, err)
	}
	return s
}

func countQueueRows(t *testing.T, db *sqldb.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
