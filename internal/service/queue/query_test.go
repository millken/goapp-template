package queue

import (
	"context"
	"maps"
	"testing"
)

// The list screen's three numbers come out of one grouped read and are summed in Go,
// so what used to be the database's answer is now this package's arithmetic. These
// pin it.

// The filtered total has to be exactly what a COUNT(*) with the same WHERE would say.
//
// It is derived by summing the cells of a status×kind grid rather than counted, which
// is only exact because those two are the only filters TaskFilter has. Comparing
// against the query it replaced is the assertion that keeps it honest — a third filter
// added to TaskFilter without touching totalOf fails here.
func TestListTasks_TotalAgreesWithACountQuery(t *testing.T) {
	db := openMigrated(t, memDSN("counts-total"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("a", noop)
	reg.Handle("b", noop)
	s, _ := newTestService(t, db, reg, nil)

	for _, sd := range []seed{
		{Kind: "a", Status: StatusPending},
		{Kind: "a", Status: StatusPending},
		{Kind: "a", Status: StatusDead},
		{Kind: "b", Status: StatusDead},
		{Kind: "b", Status: StatusSucceeded},
		{Kind: "gone", Status: StatusPending}, // no handler registered
		{Kind: "gone", Status: StatusSucceeded},
	} {
		seedTask(t, db, sd)
	}

	for _, f := range []TaskFilter{
		{},
		{Status: StatusPending},
		{Status: StatusDead},
		{Kind: "a"},
		{Kind: "gone"},
		{Status: StatusPending, Kind: "a"},
		{Status: StatusRunning},            // a status with no rows at all
		{Status: StatusDead, Kind: "gone"}, // a combination with no rows
	} {
		page, err := s.ListTasks(ctx, f)
		if err != nil {
			t.Fatalf("ListTasks(%+v): %v", f, err)
		}
		where, args := taskFilterSQL(f.Status, f.Kind)
		want := countRows(t, db, `SELECT COUNT(*) FROM queue_tasks`+where, args...)
		if page.Total != want {
			t.Errorf("ListTasks(%+v).Total = %d, want %d (what COUNT(*) says)", f, page.Total, want)
		}
	}
}

// The summary line and the orphan banner describe the WHOLE table, not the filtered
// page: seeing "已失败 400" while looking at a list filtered to pending is the point of
// having them.
func TestListTasks_CountsTheWholeTableEvenWhenFiltered(t *testing.T) {
	db := openMigrated(t, memDSN("counts-whole"))
	ctx := context.Background()

	reg := NewRegistry()
	reg.Handle("known", noop)
	s, _ := newTestService(t, db, reg, nil)

	for _, sd := range []seed{
		{Kind: "known", Status: StatusPending},
		{Kind: "known", Status: StatusDead},
		{Kind: "known", Status: StatusDead},
		{Kind: "orphan", Status: StatusPending},
		{Kind: "orphan", Status: StatusPending},
		// Terminal rows of an unhandled kind are NOT orphans: nothing is waiting to
		// run them, so warning about them would be noise that never clears.
		{Kind: "orphan", Status: StatusSucceeded},
	} {
		seedTask(t, db, sd)
	}

	page, err := s.ListTasks(ctx, TaskFilter{Status: StatusPending, Kind: "known"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("Total = %d, want 1: the total follows the filter", page.Total)
	}

	wantCounts := map[string]int{
		StatusPending: 3, StatusRunning: 0, StatusSucceeded: 1,
		StatusDead: 2, StatusCancelled: 0,
	}
	if !maps.Equal(page.StatusCounts, wantCounts) {
		t.Errorf("StatusCounts = %v, want %v", page.StatusCounts, wantCounts)
	}
	// Every status is present even at zero, so the summary line does not change shape
	// as the queue drains.
	if _, ok := page.StatusCounts[StatusRunning]; !ok {
		t.Error("a status with no tasks was dropped from the summary")
	}

	if !maps.Equal(page.Orphans, map[string]int{"orphan": 2}) {
		t.Errorf("Orphans = %v, want orphan:2 (pending only, and only unhandled kinds)",
			page.Orphans)
	}
}

// The Go derivation of "orphan" has to agree with the SQL one the scheduler's tick and
// the CLI use. Two implementations of one rule is the price of asking the cheap
// pending-only question on a tick and the whole-grid question on a page view; this is
// what stops them drifting.
func TestOrphansOf_AgreesWithTheSQLOrphanCounts(t *testing.T) {
	db := openMigrated(t, memDSN("counts-orphan-parity"))
	ctx := context.Background()

	for _, sd := range []seed{
		{Kind: "known", Status: StatusPending},
		{Kind: "gone", Status: StatusPending},
		{Kind: "gone", Status: StatusPending},
		{Kind: "gone", Status: StatusDead},
		{Kind: "also-gone", Status: StatusPending},
	} {
		seedTask(t, db, sd)
	}

	for _, known := range [][]string{{"known"}, {"known", "gone"}, nil} {
		cells, err := taskCounts(ctx, db)
		if err != nil {
			t.Fatalf("taskCounts: %v", err)
		}
		want, err := orphanCounts(ctx, db, known)
		if err != nil {
			t.Fatalf("orphanCounts: %v", err)
		}
		if got := orphansOf(cells, known); !maps.Equal(got, want) {
			t.Errorf("known=%v: orphansOf = %v, orphanCounts = %v", known, got, want)
		}
	}
}
