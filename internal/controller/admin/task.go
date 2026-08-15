package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/millken/goapp-template/internal/service/queue"
	"github.com/millken/inertia"
)

// The task queue screens: a list, a detail page with the failure log, and four
// operator actions.
//
// Two resources, not one — `task` here and `cron` in cron.go. Changing a cron
// expression can make a job fire every second; retrying one failed task cannot. Those
// are different blast radii and an operator should be able to hold one permission
// without the other.
//
// The handlers are thin on purpose. Every statement against the queue's tables lives
// in internal/service/queue, which is a deliberate departure from user_crud.go writing
// its own SQL: the admins table belongs to this area, the queue's tables do not, and
// every write to them has to respect the same lease fencing and status invariants the
// worker does. A "retry" query copied in here that forgot one predicate is how a
// running task gets quietly resurrected.

// mountTasks registers the task resource. Everything goes through the registrar, so
// task.access guards the reads and task.modify the writes without either being named,
// and the sidebar entry is gated by the same key.
//
// Deliberately absent: any route that creates a task. A task's kind has to have a
// handler registered in code, so there is nothing sensible for a "new task" form to
// offer — and the absence of the route is a stronger statement of that than a check
// inside one would be.
//
// This must not read anything from a.Services: permission_test.go calls Mount on a nil
// *app.Services, which is what keeps the mount path free of service dependencies. Only
// a.Prefix() is safe here.
func (a *Admin) mountTasks(eng *inertia.Engine) {
	base := a.taskBase()
	r := a.Resource(eng, "task")

	r.GET(base, a.tasksIndex)
	r.GET(base+"/:id", a.taskShow)
	r.POST(base+"/:id/retry", a.taskRetry)
	r.POST(base+"/:id/run", a.taskRunNow)
	r.POST(base+"/:id/cancel", a.taskCancel)
	r.POST(base+"/:id/delete", a.taskDelete)
	// "系统" already has an icon in AdminShell's sectionIcons. The two queue entries
	// sort by title within it — 任务 before 定时任务 by code point — which is the order
	// wanted; Registrar.Menu takes no explicit order and giving it one would ripple
	// into the scaffold templates for no gain here.
	r.Menu("系统", "任务", base)
}

func (a *Admin) taskBase() string { return a.Prefix() + "/task" }

// taskRow is one list row as the page receives it.
//
// A view struct rather than queue.TaskListItem passed straight through, because of the
// *_text fields: timestamps are formatted HERE, in Go. Two reasons, and the first is
// not negotiable — the SSR runtime is QuickJS, which has no Intl, so toLocaleString is
// unavailable server-side. And even hand-rolled formatting would read the machine's
// timezone, which for QuickJS is the server's and for the browser is the user's, so the
// same prop would render two different strings and hydration would disagree with itself.
//
// The raw UnixNano rides along because the page still needs to test for "not set".
type taskRow struct {
	queue.TaskListItem
	RunAtText     string `json:"run_at_text"`
	CreatedAtText string `json:"created_at_text"`
	FinishedText  string `json:"finished_at_text"`
}

// taskDetailRow is the detail page's model.
type taskDetailRow struct {
	queue.TaskDetail
	RunAtText      string `json:"run_at_text"`
	CreatedAtText  string `json:"created_at_text"`
	StartedText    string `json:"started_at_text"`
	FinishedText   string `json:"finished_at_text"`
	LeaseUntilText string `json:"lease_until_text"`
}

// attemptRow is one row of the failure log.
type attemptRow struct {
	queue.Attempt
	StartedText  string `json:"started_at_text"`
	FinishedText string `json:"finished_at_text"`
	// DurationMS is computed here rather than stored: it is a presentation of two
	// columns that are already there.
	DurationMS int64 `json:"duration_ms"`
}

// tasksIndex renders one page of the task list.
//
// Server-side filtering and paging, driven by the query string, with no fetch anywhere:
// PJAX turns a GET form into a bookmarkable URL and an <a> into a navigation, so a
// filtered list is shareable and the page carries no network code. That is the opposite
// trade from the file manager, which uses fetch precisely so that browsing directories
// does NOT enter history.
func (a *Admin) tasksIndex(c *inertia.Context) {
	ctx := c.Request.Context()

	// "Jump to id" instead of a free-text search. The only free text worth searching is
	// last_error, and that is an unindexed LIKE across the largest table in the
	// schema; jumping to an id from a log line is what an operator actually does, and
	// it is a primary-key lookup.
	if raw := c.Query("goto"); raw != "" {
		a.taskGoto(c, raw)
		return
	}

	filter := queue.TaskFilter{
		Status:   c.Query("status"),
		Kind:     c.Query("kind"),
		Page:     atoiDefault(c.Query("page"), 1),
		PageSize: a.Queue.AdminPageSize(),
	}
	page, err := a.Queue.ListTasks(ctx, filter)
	if err != nil {
		slog.Error("admin: list tasks", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	items := make([]taskRow, 0, len(page.Items))
	for _, it := range page.Items {
		items = append(items, taskRow{
			TaskListItem:  it,
			RunAtText:     unixNanoText(it.RunAt),
			CreatedAtText: unixNanoText(it.CreatedAt),
			FinishedText:  unixNanoText(it.FinishedAt),
		})
	}

	c.Set("items", items)
	c.Set("total", page.Total)
	// page comes back from ListTasks rather than from the query string: it clamps an
	// out-of-range request, and the pager has to agree with the rows it is paging.
	c.Set("page", page.Page)
	c.Set("pageSize", page.PageSize)
	// The filter ListTasks applied, not the one the URL asked for: a status it
	// dropped must not come back looking like it is in force.
	c.Set("filters", map[string]string{"status": page.Status, "kind": page.Kind})
	c.Set("kinds", a.Queue.Kinds())
	// Both come out of the same grouped read ListTasks already did. Orphans get their
	// own banner: such a task is never claimed, so without saying so the list just
	// shows something sitting still for no stated reason.
	c.Set("counts", page.StatusCounts)
	c.Set("orphans", page.Orphans)
	c.Set("basePath", a.taskBase())
	if err := c.Render("admin/task/index"); err != nil {
		slog.Error("render admin task index", "err", err)
	}
}

// taskGoto redirects to a task by id, or explains why it could not.
//
// A miss flashes and falls through to the list rather than 404ing: the id came from a
// text box, so a typo is the likely cause and an error page is a dead end.
func (a *Admin) taskGoto(c *inertia.Context, raw string) {
	id, err := parseInt64(raw)
	if err != nil || id <= 0 {
		a.flash(c, "error", fmt.Sprintf("%q 不是任务编号", raw))
		a.redirect(c, a.taskBase())
		return
	}
	task, err := a.Queue.Task(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: find task", "err", err, "task", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if task == nil {
		a.flash(c, "error", fmt.Sprintf("找不到任务 #%d", id))
		a.redirect(c, a.taskBase())
		return
	}
	a.redirect(c, fmt.Sprintf("%s/%d", a.taskBase(), id))
}

// attemptListCap bounds the failure log a detail page renders. max_attempts is usually
// under ten, but a task retried by hand often enough accumulates far more.
const attemptListCap = 50

// taskShow renders one task with its failure log.
func (a *Admin) taskShow(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	task, err := a.Queue.Task(ctx, id)
	if err != nil {
		slog.Error("admin: find task", "err", err, "task", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if task == nil {
		// The (nil, nil) contract, same as findGroupRow: a missing row is a 404 and a
		// database failure is a 500, with no error inspection at the call site.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	attempts, err := a.Queue.Attempts(ctx, id, attemptListCap)
	if err != nil {
		slog.Error("admin: task attempts", "err", err, "task", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// Only worth a second round trip when the list was capped. Below the cap the rows
	// in hand ARE the whole log, and max_attempts defaults to 3, so on almost every
	// task this query would exist to count what was just returned.
	total := len(attempts)
	if len(attempts) == attemptListCap {
		total, err = a.Queue.AttemptCount(ctx, id)
		if err != nil {
			slog.Error("admin: count task attempts", "err", err, "task", id)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	rows := make([]attemptRow, 0, len(attempts))
	for _, at := range attempts {
		rows = append(rows, attemptRow{
			Attempt:      at,
			StartedText:  unixNanoText(at.StartedAt),
			FinishedText: unixNanoText(at.FinishedAt),
			DurationMS:   durationMS(at.StartedAt, at.FinishedAt),
		})
	}

	c.Set("item", taskDetailRow{
		TaskDetail:     *task,
		RunAtText:      unixNanoText(task.RunAt),
		CreatedAtText:  unixNanoText(task.CreatedAt),
		StartedText:    unixNanoText(task.StartedAt),
		FinishedText:   unixNanoText(task.FinishedAt),
		LeaseUntilText: unixNanoText(task.LeaseUntil),
	})
	c.Set("attempts", rows)
	c.Set("attemptTotal", total)
	c.Set("attemptCap", attemptListCap)
	c.Set("basePath", a.taskBase())
	if err := c.Render("admin/task/show"); err != nil {
		slog.Error("render admin task show", "err", err)
	}
}

// --- operator actions ---
//
// All four are plain form POSTs, no fetch: each is "change one thing, then re-render",
// which is the same shape as userSetStatus. Using fetch would drag in the file
// manager's fmOK/fmFail status-code vocabulary, and nothing here has the batch or
// per-item failures that vocabulary exists for.
//
// A refusal is always a flash and a redirect, never an `errors` prop. `errors` is for
// re-rendering a form so somebody can fix their input; these actions have no fields to
// refill.
//
// Every precondition lives in the queue package's WHERE clause, and a refusal arrives
// as ok=false. Never a SELECT followed by an UPDATE — that is a race in which a task
// claimed between the two gets overwritten under the worker running it. Same shape
// groupDelete uses to refuse a group that still has members.

// taskRetry requeues a finished or cancelled task.
func (a *Admin) taskRetry(c *inertia.Context) {
	a.taskAction(c, "retry", func(id int64) (bool, error) {
		// One further attempt. The count is not reset: it is the audit trail, and
		// queue_attempts is numbered from it, so zeroing would show attempt 1 twice.
		return a.Queue.RetryTask(c.Request.Context(), id, 1)
	}, "任务已重新入队", "该任务当前状态不能重试")
}

// taskRunNow brings a task's schedule forward.
//
// The flash wording is careful: nothing is executed inline. Running it here would hold
// the HTTP request open for the handler's whole duration, run it in the web process
// rather than a worker, and bypass the lease so a polling worker could run it too.
func (a *Admin) taskRunNow(c *inertia.Context) {
	a.taskAction(c, "run", func(id int64) (bool, error) {
		return a.Queue.RunTaskNow(c.Request.Context(), id)
	}, "已排入队列，将立即执行", "该任务当前状态不能立即执行")
}

// taskCancel cancels a task, including one that is running.
//
// Cancelling a running task takes effect within one heartbeat: the status change makes
// the owner's next lease renewal skip the row, and that is what cancels the handler's
// context. The message says so rather than implying the work stopped on the click.
func (a *Admin) taskCancel(c *inertia.Context) {
	a.taskAction(c, "cancel", func(id int64) (bool, error) {
		return a.Queue.CancelTask(c.Request.Context(), id)
	}, "已请求取消；若任务正在执行，最多一个心跳周期后生效", "该任务已结束，无需取消")
}

// taskDelete removes a task and its failure log.
//
// The only irreversible action here, and the only one behind a ConfirmDialog. It
// refuses a running task: deleting the row a worker holds would make its completion
// write fail with nobody watching, and the operator almost certainly meant to cancel
// first.
func (a *Admin) taskDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	ok, err := a.Queue.DeleteTask(ctx, id)
	if err != nil {
		slog.Error("admin: delete task", "err", err, "task", id)
		a.flash(c, "error", "删除失败，请查看日志")
	} else if !ok {
		a.flash(c, "error", "正在执行的任务不能删除，请先取消")
	} else {
		a.flash(c, "success", "任务已删除")
	}
	// Always back to the list: the detail page of a deleted task does not exist, so
	// honouring `back=detail` here would land on a 404.
	a.redirect(c, a.taskReturn(c, 0))
}

// taskAction is the shared shape of the three non-destructive actions: run it, flash
// the outcome, return where the operator was.
func (a *Admin) taskAction(
	c *inertia.Context, what string,
	run func(id int64) (bool, error),
	okMsg, refusedMsg string,
) {
	id, _ := c.Params.GetInt64("id")

	ok, err := run(id)
	switch {
	case err != nil:
		slog.Error("admin: task action failed", "action", what, "task", id, "err", err)
		a.flash(c, "error", "操作失败，请查看日志")
	case !ok:
		// Either the task is gone or its status moved on since the page was rendered.
		// A stale page is the common case and it is not an error.
		a.flash(c, "error", refusedMsg)
	default:
		a.flash(c, "success", okMsg)
	}
	a.redirect(c, a.taskReturn(c, id))
}

// --- shared view helpers ---

// unixNanoText renders a UnixNano stamp for display.
//
// Formatted in Go, not in the page, and this is a constraint rather than a preference.
// The SSR runtime is QuickJS, which carries no Intl, so toLocaleString is unavailable
// server-side. Hand-rolled formatting would not help either: new Date() reads the
// machine's timezone, which is the server's under SSR and the user's in the browser, so
// one prop would render two different strings and hydration would disagree with itself.
// Using getUTC* everywhere would fix that by making every operator read UTC.
//
// Zero renders empty rather than 1970: a task that has never run must not look as
// though it ran at the epoch.
//
// Deliberately absolute, never relative ("3 minutes ago"). A relative stamp is stale
// the instant it is rendered, so it either has to live in the browser — back to the
// problems above — or be wrong. This is the first date formatting in the repository's
// frontend, so it is also the precedent.
func unixNanoText(ns int64) string {
	if ns == 0 {
		return ""
	}
	return time.Unix(0, ns).Format("2006-01-02 15:04:05")
}

// durationMS is how long an attempt took. Computed rather than stored: it is a
// presentation of two columns that are already there.
func durationMS(startedAt, finishedAt int64) int64 {
	if startedAt == 0 || finishedAt <= startedAt {
		return 0
	}
	return (finishedAt - startedAt) / int64(time.Millisecond)
}

// atoiDefault parses a small positive integer from a query parameter, falling back to
// def. Out-of-range page numbers are clamped downstream by ListTasks, so this only has
// to reject nonsense.
func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := parseInt64(s)
	if err != nil || n < 1 {
		return def
	}
	return int(n)
}

// taskReturn is where an action sends the operator afterwards.
//
// It is built from keys this handler recognises, never from a submitted location.
// There is no open-redirect surface: every byte of the result is either a constant or a
// value just parsed and validated here. A `back` field carrying a URL, or trusting
// Referer, would be exactly that surface.
//
// The point is that an action taken from page 3 of a filtered list returns to page 3 of
// that filtered list, rather than dumping the operator back at the top.
func (a *Admin) taskReturn(c *inertia.Context, id int64) string {
	if c.Query("back") == "detail" {
		return fmt.Sprintf("%s/%d", a.taskBase(), id)
	}
	q := url.Values{}
	if s := c.Query("status"); queue.ValidStatus(s) {
		q.Set("status", s)
	}
	// Shape, not registry membership — the same test ListTasks applies. A kind with
	// no handler here still filters the list, so requiring one would return the
	// operator to a wider list than the one they acted from; requiring the shape is
	// what keeps a hand-written value out of the header.
	if k := c.Query("kind"); queue.ValidKind(k) {
		q.Set("kind", k)
	}
	if p := c.Query("page"); p != "" && atoiDefault(p, 0) > 0 {
		q.Set("page", p)
	}
	if len(q) == 0 {
		return a.taskBase()
	}
	return a.taskBase() + "?" + q.Encode()
}
