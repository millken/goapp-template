package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// The read side: what the admin screens and the CLI show.
//
// These return raw rows — UnixNano integers, not formatted strings. Presentation
// belongs to the caller: the admin controllers render timestamps in Go (the SSR VM
// has no Intl and does not share the browser's timezone, so a client-side formatter
// would be a hydration mismatch), and the CLI renders them differently again. A
// service that pre-formatted for one of them would be wrong for the other.
//
// They live in this package rather than as SQL in the controllers — a departure from
// internal/controller/admin/user_crud.go, which writes its own. The reason is
// ownership: the admins table belongs to the admin area, these tables belong here,
// and the status vocabulary they filter on is defined here. Splitting the queries
// across two packages is how a screen ends up disagreeing with the worker about what
// "failed" means.

// TaskFilter selects and pages the task list. A zero value means "everything, first
// page".
type TaskFilter struct {
	// Status filters exactly, or matches everything when empty. An unrecognised
	// value is treated as empty rather than as "no results": it can only come from a
	// hand-edited URL, and an empty list would look like an empty queue. TaskPage
	// reports what was actually applied, so a caller can echo that instead of the
	// value it sent.
	Status string
	// Kind filters exactly, or matches everything when empty. Checked against
	// ValidKind — the shape — and not against the registry: filtering on a kind with
	// no handler here is the whole point of the orphan banner, and dropping it turned
	// "show me this kind" into "show me everything".
	Kind string
	// Page is 1-based. Out-of-range values are clamped by ListTasks rather than
	// refused, so a bookmarked page 40 of a list that has shrunk shows the last page
	// instead of nothing.
	Page int
	// PageSize is rows per page; zero falls back to the service default.
	PageSize int
}

// TaskListItem is one row of the task list. Deliberately narrower than TaskDetail:
// the list must not carry a payload or a full error per row.
type TaskListItem struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	Priority    int    `json:"priority"`
	RunAt       int64  `json:"run_at"`
	CreatedAt   int64  `json:"created_at"`
	FinishedAt  int64  `json:"finished_at"`
	// LastError is the first line only. The list needs enough to recognise a
	// failure, and a page of stack traces is not a list.
	LastError string `json:"last_error"`
	// ScheduleID is the cron plan that produced this task, or 0.
	ScheduleID int64 `json:"schedule_id"`
	// KnownKind reports whether this process has a handler for Kind. False means the
	// task will never be claimed here, which is the single most useful thing to show
	// about a task that is not moving.
	KnownKind bool `json:"known_kind"`
}

// TaskPage is one page of the task list plus everything the screen around it shows.
//
// The counts are here rather than behind their own methods because they all come out
// of one grouped read (see taskCounts): Total, StatusCounts and Orphans are three sums
// over the same grid, and returning them together is what keeps the list screen down
// to two statements instead of four.
type TaskPage struct {
	Items    []TaskListItem `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	// Status and Kind are the filter ListTasks actually applied, which is not always
	// the one it was given: a status outside the vocabulary is dropped. Returned for
	// the same reason Page is — the caller has to describe the rows it got back, and
	// echoing the request instead labels an unfiltered list as filtered.
	Status string `json:"status"`
	Kind   string `json:"kind"`
	// StatusCounts is every status counted over the WHOLE table, filter or no filter.
	// The summary line is how an operator sees that 400 tasks are dead while looking
	// at a list filtered to pending.
	StatusCounts map[string]int `json:"status_counts"`
	// Orphans is pending tasks per kind this process has no handler for. Never
	// claimed, so without saying so the list just shows something sitting still.
	Orphans map[string]int `json:"orphans"`
}

// TaskDetail is one task in full.
type TaskDetail struct {
	TaskListItem
	Payload    string `json:"payload"`
	TimeoutMS  int    `json:"timeout_ms"`
	StartedAt  int64  `json:"started_at"`
	UpdatedAt  int64  `json:"updated_at"`
	LeaseUntil int64  `json:"lease_until"`
	Worker     string `json:"worker"`
	UniqueKey  string `json:"unique_key"`
	// FullError is last_error untruncated by the list's first-line rule.
	FullError string `json:"full_error"`
}

// Attempt is one row of the failure log.
type Attempt struct {
	Attempt    int    `json:"attempt"`
	Outcome    string `json:"outcome"`
	Worker     string `json:"worker"`
	Error      string `json:"error"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// ScheduleView is one cron plan as the admin list shows it.
type ScheduleView struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Payload  string `json:"payload"`
	Spec     string `json:"spec"`
	CodeSpec string `json:"code_spec"`
	Enabled  bool   `json:"enabled"`
	// Present is false for a plan the code no longer declares. Such a plan never
	// fires; it is kept so an operator's expression edit and its history survive a
	// deployment that happened to be missing a handler.
	Present     bool  `json:"present"`
	Drifted     bool  `json:"drifted"`
	NextRunAt   int64 `json:"next_run_at"`
	LastFireAt  int64 `json:"last_fire_at"`
	LastTaskID  int64 `json:"last_task_id"`
	MaxAttempts int   `json:"max_attempts"`
	// LastStatus is the status of the task the last firing produced, or "" if there
	// has not been one. Read through last_task_id, which is a primary-key join —
	// the column exists precisely so this is not a per-row "latest task of this
	// kind" query.
	LastStatus string `json:"last_status"`
	// KnownKind reports whether this process has a handler for Kind.
	KnownKind bool `json:"known_kind"`
}

// ValidStatus reports whether s is a status this package stores. Exported so the
// admin layer can reject a hand-edited filter value without repeating the list.
func ValidStatus(s string) bool {
	switch s {
	case StatusPending, StatusRunning, StatusSucceeded, StatusDead, StatusCancelled:
		return true
	default:
		return false
	}
}

// ValidKind reports whether s is shaped like a kind this package would accept from
// Handle. Exported for the same reason as ValidStatus: a caller filtering or
// round-tripping a kind needs to reject a hand-edited value without repeating the
// rule.
//
// Deliberately NOT "is registered here". A kind with no handler in this process is
// precisely the one worth filtering on — the orphan banner names them and the next
// thing an operator does is list them — so membership is the wrong question. The
// shape is the right one: it is what keeps a hostile value out of a log line, a
// query parameter or a filter that claims to be in force.
func ValidKind(s string) bool {
	return kindRe.MatchString(s)
}

// KnownKind reports whether this process has a handler for kind.
func (s *Service) KnownKind(kind string) bool {
	_, ok := s.reg.lookup(kind)
	return ok
}

// ListTasks returns one page of tasks, newest first, with the counts the screen
// around the list shows.
//
// Newest first, and paged by OFFSET. Keyset pagination would scale better, but it
// cannot express a page number, and the actual use of this screen is "filter, then
// look at the first few pages" plus jumping to a known id. The seam is this one
// query.
//
// Two statements: the grid of counts, and the page of rows. The filtered total is a
// sum over the grid rather than its own COUNT(*), which is exact — status and kind are
// the only filters, and both are dimensions of the grid.
func (s *Service) ListTasks(ctx context.Context, f TaskFilter) (TaskPage, error) {
	pageSize := f.PageSize
	if pageSize <= 0 {
		pageSize = s.AdminPageSize()
	}
	// A filter value that is not one this package could have produced is dropped
	// rather than honoured — it can only come from a hand-edited URL, and returning
	// nothing would read as an empty queue. Kind is checked for shape, not for
	// membership in the registry: see TaskFilter.Kind.
	status := f.Status
	if status != "" && !ValidStatus(status) {
		status = ""
	}
	kind := f.Kind
	if kind != "" && !ValidKind(kind) {
		kind = ""
	}

	where, args := taskFilterSQL(status, kind)

	cells, err := taskCounts(ctx, s.db)
	if err != nil {
		return TaskPage{}, err
	}
	total := totalOf(cells, status, kind)

	page := f.Page
	if page < 1 {
		page = 1
	}
	if pages := (total + pageSize - 1) / pageSize; pages > 0 && page > pages {
		page = pages
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, status, attempts, max_attempts, priority, run_at, created_at,
		        finished_at, last_error, schedule_id
		   FROM queue_tasks`+where+`
		  ORDER BY id DESC
		  LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return TaskPage{}, fmt.Errorf("queue: list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Never nil: the frontend expects [] rather than null for an empty page.
	items := []TaskListItem{}
	for rows.Next() {
		var it TaskListItem
		var scheduleID sql.NullInt64
		if err := rows.Scan(&it.ID, &it.Kind, &it.Status, &it.Attempts, &it.MaxAttempts,
			&it.Priority, &it.RunAt, &it.CreatedAt, &it.FinishedAt, &it.LastError,
			&scheduleID); err != nil {
			return TaskPage{}, fmt.Errorf("queue: scan task: %w", err)
		}
		it.ScheduleID = scheduleID.Int64
		it.LastError = firstLine(it.LastError)
		it.KnownKind = s.KnownKind(it.Kind)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return TaskPage{}, fmt.Errorf("queue: list tasks: %w", err)
	}
	return TaskPage{
		Items: items, Total: total, Page: page, PageSize: pageSize,
		Status: status, Kind: kind,
		StatusCounts: statusCountsOf(cells),
		Orphans:      orphansOf(cells, s.reg.Kinds()),
	}, nil
}

// totalOf sums the cells a status/kind filter selects; an empty one matches anything.
//
// This is what makes the separate COUNT(*) unnecessary. It is exact rather than an
// approximation because status and kind are the only two filters TaskFilter has, and
// both are dimensions the grid is grouped by — a filter on any third column would have
// to go back to the database, and this would be the wrong shortcut.
func totalOf(cells []taskCell, status, kind string) int {
	n := 0
	for _, c := range cells {
		if (status == "" || c.Status == status) && (kind == "" || c.Kind == kind) {
			n += c.Count
		}
	}
	return n
}

// statusCountsOf collapses the grid onto the status axis.
//
// Every status is present, including the ones with no rows: the summary line reads
// "成功 0" rather than losing an entry, and a status that vanishes when it hits zero
// makes the line jump about as an operator works.
func statusCountsOf(cells []taskCell) map[string]int {
	out := map[string]int{
		StatusPending: 0, StatusRunning: 0, StatusSucceeded: 0,
		StatusDead: 0, StatusCancelled: 0,
	}
	for _, c := range cells {
		out[c.Status] += c.Count
	}
	return out
}

// orphansOf picks out the pending cells whose kind is not in known.
//
// The same question orphanCounts asks in SQL, answered here from counts already in
// hand — see the note on orphanCounts for why both exist. An empty known means every
// pending kind is an orphan, which is the right answer for a process with no handlers.
func orphansOf(cells []taskCell, known []string) map[string]int {
	out := map[string]int{}
	for _, c := range cells {
		if c.Status == StatusPending && !slices.Contains(known, c.Kind) {
			out[c.Kind] += c.Count
		}
	}
	return out
}

// taskFilterSQL builds the WHERE for the page of rows.
func taskFilterSQL(status, kind string) (string, []any) {
	where := ""
	args := []any{}
	add := func(clause string, arg any) {
		if where == "" {
			where = " WHERE " + clause
		} else {
			where += " AND " + clause
		}
		args = append(args, arg)
	}
	if status != "" {
		add("status = ?", status)
	}
	if kind != "" {
		add("kind = ?", kind)
	}
	return where, args
}

// Task returns one task, or (nil, nil) when it does not exist.
//
// The (nil, nil) shape matches findGroupRow: it lets the HTTP layer answer 404 for a
// missing row and 500 for a database failure, without inspecting an error.
func (s *Service) Task(ctx context.Context, id int64) (*TaskDetail, error) {
	var d TaskDetail
	var scheduleID sql.NullInt64
	var uniqueKey sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, status, attempts, max_attempts, priority, run_at, created_at,
		        finished_at, last_error, schedule_id, payload, timeout_ms, started_at,
		        updated_at, lease_until, worker, unique_key
		   FROM queue_tasks WHERE id = ?`, id).
		Scan(&d.ID, &d.Kind, &d.Status, &d.Attempts, &d.MaxAttempts, &d.Priority,
			&d.RunAt, &d.CreatedAt, &d.FinishedAt, &d.FullError, &scheduleID,
			&d.Payload, &d.TimeoutMS, &d.StartedAt, &d.UpdatedAt, &d.LeaseUntil,
			&d.Worker, &uniqueKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: find task %d: %w", id, err)
	}

	d.ScheduleID = scheduleID.Int64
	d.UniqueKey = uniqueKey.String
	d.LastError = firstLine(d.FullError)
	d.KnownKind = s.KnownKind(d.Kind)
	return &d, nil
}

// Attempts returns a task's failure log, newest first, capped at limit.
//
// Newest first because the question is almost always "why did it stop", and capped
// because a task retried by hand enough times can accumulate hundreds of rows while
// max_attempts stays at three.
func (s *Service) Attempts(ctx context.Context, taskID int64, limit int) ([]Attempt, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT attempt, outcome, worker, error, started_at, finished_at
		   FROM queue_attempts WHERE task_id = ?
		  ORDER BY id DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("queue: attempts of task %d: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.Attempt, &a.Outcome, &a.Worker, &a.Error,
			&a.StartedAt, &a.FinishedAt); err != nil {
			return nil, fmt.Errorf("queue: scan attempt: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: attempts of task %d: %w", taskID, err)
	}
	return out, nil
}

// AttemptCount is how many attempts a task has logged, so a capped list can say what
// it is not showing.
func (s *Service) AttemptCount(ctx context.Context, taskID int64) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM queue_attempts WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		return 0, fmt.Errorf("queue: count attempts of task %d: %w", taskID, err)
	}
	return n, nil
}

// OrphanCounts is the number of pending tasks per kind that this process cannot run.
//
// Surfaced so the list page can say so: such a task is never claimed, so without a
// banner it simply sits there looking stuck for no stated reason.
func (s *Service) OrphanCounts(ctx context.Context) (map[string]int, error) {
	return orphanCounts(ctx, s.db, s.reg.Kinds())
}

// Schedules returns every cron plan, including ones the code no longer declares.
func (s *Service) Schedules(ctx context.Context) ([]ScheduleView, error) {
	// LEFT JOIN, not an inner one: a plan that has never fired has last_task_id 0,
	// and an inner join would drop exactly the plans somebody is most likely looking
	// for.
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.name, s.kind, s.payload, s.spec, s.code_spec, s.enabled, s.present,
		        s.next_run_at, s.last_fire_at, s.last_task_id, s.max_attempts,
		        COALESCE(t.status, '')
		   FROM queue_schedules s
		   LEFT JOIN queue_tasks t ON t.id = s.last_task_id
		  ORDER BY s.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("queue: list schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ScheduleView{}
	for rows.Next() {
		var v ScheduleView
		var enabled, present int
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Payload, &v.Spec, &v.CodeSpec,
			&enabled, &present, &v.NextRunAt, &v.LastFireAt, &v.LastTaskID,
			&v.MaxAttempts, &v.LastStatus); err != nil {
			return nil, fmt.Errorf("queue: scan schedule: %w", err)
		}
		v.Enabled, v.Present = enabled != 0, present != 0
		v.Drifted = v.Spec != v.CodeSpec
		v.KnownKind = s.KnownKind(v.Kind)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: list schedules: %w", err)
	}
	return out, nil
}

// Schedule returns one plan, or (nil, nil) when it does not exist.
func (s *Service) Schedule(ctx context.Context, id int64) (*ScheduleView, error) {
	all, err := s.Schedules(ctx)
	if err != nil {
		return nil, err
	}
	// Scanned in Go rather than fetched by id. Plans are a handful of rows — a
	// project with enough of them for this to matter has a different problem — and
	// one query means the list and the form can never disagree about a plan's shape.
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, nil
}

// NextRuns returns the next n activations of expr, for the schedule form's preview.
//
// Computed server-side, from the same parser the scheduler uses. The alternative —
// previewing as the operator types — needs either a cron parser in JavaScript (a new
// npm dependency) or a preview endpoint, both of which cost more than looking after
// saving.
func (s *Service) NextRuns(expr string, n int) ([]int64, error) {
	sched, err := s.ParseCron(expr)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, n)
	cur := s.now().In(s.Location())
	for range n {
		next, err := sched.Next(cur)
		if err != nil {
			// An expression that parses but cannot recur: return what was found so the
			// form can show "no further runs" rather than an error page.
			return out, nil
		}
		out = append(out, next.UnixNano())
		cur = next
	}
	return out, nil
}

// firstLine trims a message to its first line, for list display. A Go panic's error
// text is many lines and a table cell is one.
func firstLine(s string) string {
	for i := range len(s) {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
