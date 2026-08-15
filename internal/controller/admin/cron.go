package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/millken/goapp-template/internal/service/queue"
	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// The cron plan screens.
//
// The rule these implement is "the code is the source of truth, but an operator may
// change a schedule". What that means concretely is visible in which routes exist:
// there is no create and no unconditional delete, because a plan comes from a code
// registration and would be synced straight back on the next start. An operator can
// change the expression, pause it, trigger it once, and put it back to the code's
// default — and nothing else.
//
// Its own resource (`cron`) rather than sharing `task`, so the permission to change a
// schedule can be withheld from someone who may retry a failed task. Changing an
// expression can make a job fire every second; retrying one task cannot.

// mountCrons registers the cron resource.
//
// The path is /admin/cron, NOT /admin/task/cron: the latter sits at the same level as
// /admin/task/:id, where the router's :id would match the literal "cron".
//
// Deliberately absent, each one a statement rather than an omission:
//
//   - GET  <base>/new and POST <base> — a plan comes from code. With no route there is
//     nothing to bypass.
//   - POST <base>/:id/delete for a live plan — deleting one the code still declares
//     achieves nothing, since the next start syncs it back. Deletion exists only for a
//     plan already flagged as gone from the code, and the queue service enforces that
//     as well.
//
// Like mountTasks, this may not read anything from a.Services: permission_test.go calls
// Mount with a nil *app.Services.
func (a *Admin) mountCrons(eng *inertia.Engine) {
	base := a.cronBase()
	r := a.Resource(eng, "cron")

	r.GET(base, a.cronsIndex)
	// A bare plan URL redirects to its edit page. Registered mostly so the node is not
	// GET-less: with only POST on <base>/:id, a GET reaches an unset handler slot in the
	// router's radix tree and answers 500 with an ERROR log line — the same failure
	// serve.go describes for the uploads prefix. /admin/cron/new is the URL somebody
	// tries first, and a 500 there reads as a broken page rather than as "no such thing".
	r.GET(base+"/:id", a.cronShow)
	r.GET(base+"/:id/edit", a.cronEdit)
	r.POST(base+"/:id", a.cronUpdate)
	r.POST(base+"/:id/enabled", a.cronSetEnabled)
	r.POST(base+"/:id/run", a.cronRunNow)
	r.POST(base+"/:id/reset", a.cronResetSpec)
	r.POST(base+"/:id/delete", a.cronDelete)
	r.Menu("系统", "定时任务", base)
}

func (a *Admin) cronBase() string { return a.Prefix() + "/cron" }

// cronRow is one plan as the list receives it.
type cronRow struct {
	queue.ScheduleView
	NextRunText  string `json:"next_run_at_text"`
	LastFireText string `json:"last_fire_at_text"`
}

// nextRunsPreview is how many upcoming firings the edit form shows.
const nextRunsPreview = 5

func (a *Admin) cronsIndex(c *inertia.Context) {
	plans, err := a.Queue.Schedules(c.Request.Context())
	if err != nil {
		slog.Error("admin: list schedules", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	items := make([]cronRow, 0, len(plans))
	for _, p := range plans {
		items = append(items, cronRow{
			ScheduleView: p,
			NextRunText:  unixNanoText(p.NextRunAt),
			LastFireText: unixNanoText(p.LastFireAt),
		})
	}

	c.Set("items", items)
	c.Set("basePath", a.cronBase())
	// The plan's history is its tasks, which live on the other screen, so the link needs
	// that screen's base path. Whether to render the link at all is canViewTasks, set
	// by resolve (auth.go) alongside canBrowseFiles — cron.modify does not imply
	// task.access, and this handler must not spend a second group query to find out.
	c.Set("taskBasePath", a.taskBase())
	if err := c.Render("admin/cron/index"); err != nil {
		slog.Error("render admin cron index", "err", err)
	}
}

// cronShow redirects a bare plan URL to its edit page.
//
// A plan has nothing to show that the edit form does not, so a separate detail page would
// be the same page twice. The redirect exists so the URL is not a dead end — and so the
// route node has a GET handler at all, see mountCrons.
//
// A non-numeric or missing id is a 404, which is what makes /admin/cron/new answer
// sensibly instead of 500ing.
func (a *Admin) cronShow(c *inertia.Context) {
	id, ok := c.Params.GetInt64("id")
	if !ok || id <= 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	plan, err := a.Queue.Schedule(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: find schedule", "err", err, "schedule", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if plan == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.redirect(c, fmt.Sprintf("%s/%d/edit", a.cronBase(), id))
}

func (a *Admin) cronEdit(c *inertia.Context) {
	id, _ := c.Params.GetInt64("id")
	plan, err := a.Queue.Schedule(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: find schedule", "err", err, "schedule", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if plan == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.renderCronForm(c, *plan, plan.Spec, nil)
}

// cronUpdate saves a new expression.
func (a *Admin) cronUpdate(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	plan, err := a.Queue.Schedule(ctx, id)
	if err != nil {
		slog.Error("admin: find schedule", "err", err, "schedule", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if plan == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	expr := c.PostForm("expression")
	if v := a.validateCron(expr); !v.OK() {
		// Re-render the same page with the submitted value, the way every other form
		// in this area reports a bad field. The value is passed separately from the
		// plan so the input keeps what was typed rather than reverting.
		a.renderCronForm(c, *plan, expr, v.Errors())
		return
	}

	a.cronAction(c, "set-spec",
		func(id int64) (bool, error) { return a.Queue.SetScheduleSpec(ctx, id, expr) },
		"表达式已保存，下次执行时间已重新计算", scheduleGoneMsg)
}

// cronSetEnabled pauses or resumes a plan.
//
// The value comes from the request, not from negating what is stored. A form submitted
// twice — a double click, a retried request — must not flip it back; userSetStatus reads
// the value out of the request for the same reason.
func (a *Admin) cronSetEnabled(c *inertia.Context) {
	ctx := c.Request.Context()
	enabled := c.PostForm("enabled") == "1"

	okMsg := "已暂停"
	if enabled {
		okMsg = "已启用，下次执行时间已按当前表达式重新计算"
	}
	a.cronAction(c, "set-enabled",
		func(id int64) (bool, error) { return a.Queue.SetScheduleEnabled(ctx, id, enabled) },
		okMsg, scheduleGoneMsg)
}

// cronRunNow fires a plan once, immediately.
//
// It does not advance next_run_at: a manual run is an extra one, not a replacement for
// the scheduled one, and advancing would silently skip a cycle. A paused plan can still
// be triggered — "stop running this automatically" and "never run this again" are
// different requests, and verifying a plan by hand after pausing it is exactly what
// happens next.
func (a *Admin) cronRunNow(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	taskID, err := a.Queue.TriggerSchedule(ctx, id)
	switch {
	case errors.Is(err, queue.ErrScheduleAbsent):
		a.flash(c, "error", "该定时任务在当前代码中没有处理器，无法触发")
	case err != nil:
		slog.Error("admin: trigger schedule", "err", err, "schedule", id)
		a.flash(c, "error", "触发失败，请查看日志")
	case taskID == 0:
		a.flash(c, "error", scheduleGoneMsg)
	default:
		// The task id goes in the text rather than as a link. A flash value has to be a
		// flat string (store_db round-trips the session through JSON, store_memory does
		// not), and cron.modify does not imply task.access — so linking would send some
		// operators straight to a 403.
		a.flash(c, "success", fmt.Sprintf("已触发一次，任务 #%d", taskID))
	}
	a.redirect(c, a.cronBase())
}

// cronResetSpec puts a plan back on the expression the code declares. The visible
// payoff of storing code_spec: an override can be undone without knowing what the code
// said.
func (a *Admin) cronResetSpec(c *inertia.Context) {
	ctx := c.Request.Context()
	a.cronAction(c, "reset-spec",
		func(id int64) (bool, error) { return a.Queue.ResetScheduleSpec(ctx, id) },
		"已恢复为代码中的默认表达式", scheduleGoneMsg)
}

// cronDelete removes a plan the code no longer declares.
//
// The queue service refuses any other kind, so this is safe even though the UI only
// offers the button for a flagged plan.
func (a *Admin) cronDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	a.cronAction(c, "delete",
		func(id int64) (bool, error) { return a.Queue.DeleteSchedule(ctx, id) },
		"定时任务已删除", "只能删除代码中已移除的定时任务")
}

// scheduleGoneMsg is what three of the four actions say when the service refuses
// them: the plan was deleted between the page rendering and the button being
// pressed, which is a stale page rather than an error.
const scheduleGoneMsg = "该定时任务已不存在"

// cronAction is the shared shape of the plan edits: run it, flash the outcome,
// return to the list. The task screen's taskAction next door is the same helper for
// the same reason — four copies of this switch is four places to touch when the
// wording or the log key changes.
//
// cronRunNow is deliberately not built on it: it reports a task id rather than a
// bool and has a refusal of its own (a plan with no handler in this process).
func (a *Admin) cronAction(
	c *inertia.Context, what string,
	run func(id int64) (bool, error),
	okMsg, refusedMsg string,
) {
	id, _ := c.Params.GetInt64("id")

	ok, err := run(id)
	switch {
	case err != nil:
		slog.Error("admin: cron action failed", "action", what, "schedule", id, "err", err)
		a.flash(c, "error", "操作失败，请查看日志")
	case !ok:
		a.flash(c, "error", refusedMsg)
	default:
		a.flash(c, "success", okMsg)
	}
	a.redirect(c, a.cronBase())
}

// validateCron checks a submitted expression.
func (a *Admin) validateCron(expr string) *validate.Validator {
	v := validate.New()
	v.Field("expression", expr,
		validate.Required,
		validate.MaxLen(128),
		// Cheap rules first, so a blank or absurd value never reaches the parser.
		a.cronExpressionParses(),
	)
	return v
}

// cronExpressionParses delegates to the parser the scheduler itself runs on.
//
// This is the whole point of queue exposing ParseCron as a method: validating with a
// second implementation is exactly how "saved fine, never fires" happens.
func (a *Admin) cronExpressionParses() validate.Rule {
	return func(expr string) error {
		if _, err := a.Queue.ParseCron(expr); err != nil {
			return errors.New(cronErrorText(err))
		}
		return nil
	}
}

// cronErrorText strips the package prefix from a parser error for display. The message
// itself is worth showing — it names the offending field — but "queue: " in a form
// error is noise to whoever is fixing the expression.
func cronErrorText(err error) string {
	return "表达式无法解析：" + strings.TrimPrefix(err.Error(), "queue: ")
}

// renderCronForm renders the edit form.
//
// expr is passed separately from plan so a rejected submission redisplays what was
// typed instead of reverting to what is stored — the same reason group_crud.go rebinds
// the form onto the submitted item before re-rendering.
//
// The preview is computed server-side. Previewing as the operator types would need
// either a cron parser in JavaScript (a new npm dependency, and a second implementation
// of the thing that must not have two) or a preview endpoint with its own response
// shape. Neither is worth more than looking after saving.
func (a *Admin) renderCronForm(c *inertia.Context, plan queue.ScheduleView, expr string, errs map[string]string) {
	// Preview the expression being shown, which for a rejected submission is the one
	// that failed — so it yields nothing, and the page shows the field error instead.
	nextRuns := []string{}
	if raw, err := a.Queue.NextRuns(expr, nextRunsPreview); err == nil {
		for _, ns := range raw {
			nextRuns = append(nextRuns, unixNanoText(ns))
		}
	}

	c.Set("item", cronRow{
		ScheduleView: plan,
		NextRunText:  unixNanoText(plan.NextRunAt),
		LastFireText: unixNanoText(plan.LastFireAt),
	})
	// The value the input shows, distinct from item.spec, which is what is stored.
	c.Set("expression", expr)
	c.Set("nextRuns", nextRuns)
	c.Set("basePath", a.cronBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/cron/form"); err != nil {
		slog.Error("render admin cron form", "err", err)
	}
}
