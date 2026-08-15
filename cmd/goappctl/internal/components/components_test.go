package components

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestClosure(t *testing.T) {
	cases := []struct {
		name         string
		in           []string
		wantResolved []string
		wantAdded    []string
	}{
		{"empty", nil, nil, nil},
		{"independent", []string{"db"}, []string{"db"}, nil},
		{"admin pulls session and db", []string{"admin"}, []string{"db", "session", "admin"}, []string{"db", "session"}},
		{"admin with db already picked", []string{"db", "admin"}, []string{"db", "session", "admin"}, []string{"session"}},
		{"all", []string{"ssr", "admin", "session", "db"}, []string{"db", "session", "admin", "ssr"}, nil},
		// The queue is a pure database queue, so db is its only dependency. Notably NOT
		// admin: a worker with no management screens is a supported build.
		{"queue pulls db", []string{"queue"}, []string{"db", "queue"}, []string{"db"}},
		{"queue with admin", []string{"admin", "queue"},
			[]string{"db", "session", "admin", "queue"}, []string{"db", "session"}},
		{"everything", []string{"queue", "storage", "ssr", "admin"},
			[]string{"db", "session", "admin", "storage", "queue", "ssr"}, []string{"db", "session"}},
		{"blank entries ignored", []string{"db", "", " "}, []string{"db"}, nil},
		{"storage is independent", []string{"storage"}, []string{"storage"}, nil},
		{"storage with admin", []string{"admin", "storage"}, []string{"db", "session", "admin", "storage"}, []string{"db", "session"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved, added, err := Closure(c.in)
			if err != nil {
				t.Fatalf("Closure(%v): %v", c.in, err)
			}
			if !slices.Equal(resolved, c.wantResolved) {
				t.Errorf("resolved = %v, want %v", resolved, c.wantResolved)
			}
			if !slices.Equal(added, c.wantAdded) {
				t.Errorf("added = %v, want %v", added, c.wantAdded)
			}
		})
	}
}

// TestClosure_ResolvedIsInChecklistOrder pins that resolved is dependency-safe
// to iterate: db and session precede admin, which needs both.
func TestClosure_ResolvedIsInChecklistOrder(t *testing.T) {
	resolved, _, err := Closure([]string{"admin"})
	if err != nil {
		t.Fatalf("Closure: %v", err)
	}
	adminAt := slices.Index(resolved, "admin")
	for _, dep := range []string{"db", "session"} {
		if at := slices.Index(resolved, dep); at < 0 || at > adminAt {
			t.Errorf("%s at %d must precede admin at %d in %v", dep, at, adminAt, resolved)
		}
	}
}

func TestClosure_Errors(t *testing.T) {
	if _, _, err := Closure([]string{"redis"}); err == nil {
		t.Error("expected an error for an unknown component")
	}
	if _, _, err := Closure([]string{Tooling}); err == nil {
		t.Errorf("expected %q to be rejected as reserved", Tooling)
	}
}

func TestOff_AlwaysIncludesTooling(t *testing.T) {
	off := Off(Names())
	if !off[Tooling] {
		t.Errorf("Off must always strip %q, got %v", Tooling, off)
	}
	for _, n := range Names() {
		if off[n] {
			t.Errorf("%q is selected but marked off", n)
		}
	}

	off = Off(nil)
	for _, n := range append(Names(), Tooling) {
		if !off[n] {
			t.Errorf("%q should be off when nothing is selected", n)
		}
	}
}

// TestOwnedPathsExist is the drift guard: a renamed directory would silently
// turn a deletion into a no-op, leaving a stripped component's code behind.
// Paths are resolved against the repo root, four levels up from this package.
func TestOwnedPathsExist(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repo root not found at %s: %v", root, err)
	}
	for _, c := range All {
		for _, p := range c.Owned {
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				t.Errorf("component %q owns %q, which does not exist: %v", c.Name, p, err)
			}
		}
	}
	// Tooling paths that must exist in the template (the rest are optional
	// build artifacts, workspace files, or CI config).
	for _, p := range []string{
		"cmd/goappctl", "docs", ".github/workflows/goappctl.yml",
		"frontend/pages/admin/ssrfixture", "server/ssr_fixture_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("tooling path %q does not exist: %v", p, err)
		}
	}
}

// TestStorageOwnsItsAdminSideFiles pins the arrangement that makes "admin on,
// storage off" strip correctly: the file manager's files live under directories
// admin owns, so storage has to name them individually. Deletion is idempotent,
// so the overlap is harmless — but a missing entry here is a generated project
// that references a service it does not have.
func TestStorageOwnsItsAdminSideFiles(t *testing.T) {
	c, ok := Get("storage")
	if !ok {
		t.Fatal("no storage component")
	}
	want := []string{
		"internal/service/storage",
		"internal/controller/admin/filemanager.go",
		"internal/controller/admin/filemanager_test.go",
		"frontend/pages/admin/filemanager",
		"frontend/src/components/admin/FileManager.vue",
		"frontend/src/components/admin/FileManager.test.ts",
		"frontend/src/components/admin/FileManagerDialog.vue",
		"frontend/src/components/admin/ImagePicker.vue",
		"frontend/src/components/admin/ImagePicker.test.ts",
	}
	for _, w := range want {
		if !slices.Contains(c.Owned, w) {
			t.Errorf("storage does not own %q", w)
		}
	}
}

// TestQueueOwnsItsAdminSideFiles is the storage test's twin, and it exists for the same
// reason: the queue's management screens live under directories admin owns, so "admin on,
// queue off" only strips correctly if each of those files is named here.
//
// frontend/src/lib is admin's, so task-status.ts has to be named too — and both of its
// files, since a stripped project keeping a test for deleted code is a broken build rather
// than a cosmetic leftover.
func TestQueueOwnsItsAdminSideFiles(t *testing.T) {
	c, ok := Get("queue")
	if !ok {
		t.Fatal("no queue component")
	}
	want := []string{
		"internal/service/queue",
		"internal/tasks",
		"commands/queue.go",
		"internal/controller/admin/task.go",
		"internal/controller/admin/task_test.go",
		"internal/controller/admin/cron.go",
		"internal/controller/admin/cron_test.go",
		"frontend/pages/admin/task",
		"frontend/pages/admin/cron",
		"frontend/src/components/admin/ServerTable.vue",
		"frontend/src/components/admin/ServerTable.test.ts",
		"frontend/src/lib/task-status.ts",
		"frontend/src/lib/task-status.test.ts",
	}
	for _, w := range want {
		if !slices.Contains(c.Owned, w) {
			t.Errorf("queue does not own %q", w)
		}
	}
}

// The queue's schema travels with internal/service/queue rather than as a numbered file in
// internal/service/db/migrations, and that is load-bearing rather than tidy.
//
// sqldb's migrator keeps ONE version per migration service and skips any file whose version
// is <= it. Under a shared numbering the queue's file would claim, say, 007; a project
// generated without the queue never applies it and its mark moves on to 008, 009 — and the
// day it adds the queue back, 007 <= 009 and the file is silently skipped. No tables, and
// the failure surfaces at runtime.
func TestQueueMigrationsAreNotOwnedByDB(t *testing.T) {
	db, ok := Get("db")
	if !ok {
		t.Fatal("no db component")
	}
	for _, p := range db.Owned {
		if strings.Contains(p, "queue") {
			t.Errorf("db owns %q; the queue's schema must live under internal/service/queue "+
				"so it is recorded under its own migration service", p)
		}
	}

	queue, _ := Get("queue")
	if !slices.Contains(queue.Owned, "internal/service/queue") {
		t.Error("queue must own its whole directory, migrations included")
	}
	for _, p := range queue.Owned {
		if strings.HasPrefix(p, "internal/service/db/") {
			t.Errorf("queue owns %q inside the db component's directory; its schema is its own", p)
		}
	}
}

func TestAdminOwnsCopiedUIComponents(t *testing.T) {
	c, ok := Get("admin")
	if !ok {
		t.Fatal("admin component missing")
	}
	for _, want := range []string{"frontend/src/components/ui", "frontend/src/lib"} {
		if !slices.Contains(c.Owned, want) {
			t.Errorf("admin.Owned missing %q; a trimmed project would ship the shadcn source", want)
		}
	}
}
