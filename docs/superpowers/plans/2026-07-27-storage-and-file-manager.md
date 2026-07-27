# Storage Component and Admin File Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fifth optional component, `storage`, plus the OpenCart-style file manager that makes it visible — a media library page, a modal picker, and an avatar on the admin user that consumes it.

**Architecture:** A `Backend` interface with a single `os.Root`-backed implementation, held by a `Lifecycle` service. Reads are one public static route; writes are a JSON API inside the admin area, registered through `Registrar` so permissions and the sidebar entry come for free. One Vue component serves the page, the picker dialog, and the move-target dialog.

**Tech Stack:** Go 1.26 (`os.Root`, `mime/multipart`, `io/fs`), `github.com/millken/inertia`, Vue 3 + shadcn-vue components already in `frontend/src/components/ui`, vitest.

**Spec:** `docs/superpowers/specs/2026-07-27-storage-and-file-manager-design.md` — read it before any task. Section references below (§4.2, §5.2 …) point into it.

## Global Constraints

- **No new Go dependencies and no new npm dependencies.** Everything is stdlib plus what the repo already has. If a task seems to need one, stop and ask.
- **Path cleaning happens in `Service`, never in a `Backend`.** A backend may assume its input is clean (§4.2 semantic 1).
- **Both defences, always:** `clean()` on the way in *and* `os.Root` at the syscall. Never one on the grounds that the other exists.
- **Errors are matched with `errors.Is`, never by string.** Backends wrap `fs.ErrNotExist` / `fs.ErrExist`.
- **`.vue` files cannot carry `goappctl` markers** — `markers.forms` has no `.vue` form and a marker in an unsupported file type makes `init` fail. Vue-side stripping is done by owning whole files in `components.go`, never by a marker inside a shared `.vue`.
- **A marker must never appear inside a Go string literal.** Markers are line-based comments stripped from the source; a `//goappctl:storage` line inside a raw-string SQL statement is not a comment, it is four words of SQL. Where a query needs to differ by component, keep the query whole and mark the *Go statements* around it — or, better, let the column exist unconditionally (migration 006 is unmarked for exactly this reason) and mark only the code that needs `a.Storage`.
- **`FormField.vue` is not modified.** The picker is a control that goes in its slot.
- **Every admin route goes through `Registrar`** (`a.Resource(eng, "filemanager")`). A route registered directly would have no permission key and would not appear in the catalogue.
- **All admin copy is Chinese**, matching the existing pages (`用户`, `分组`, `新建`…).
- **Default page size is 40** for the file grid. This is not the `DataTable` default of 10; a thumbnail grid is a different surface.
- **`.svg` is not in the default `allowed_ext`** (same-origin SVG is stored XSS). Do not add it "for completeness".
- Verification, run at the end of every task: `go build ./...`, `go vet ./...`, `gofmt -l .` silent, `go test ./... -count=1`; for frontend tasks also `pnpm -C frontend run test`, `pnpm -C frontend run type-check`, `pnpm -C frontend run build`.

## Three things that shape the tasks

1. **The conformance suite is the interface's whole justification** (§7.1). Task 1 writes the suite *before* the implementation, and the suite is the test for that task. Getting it thin defeats the reason the interface exists.
2. **`backendtest` importing `storage` means `local_test.go` must be `package storage_test`.** An internal test file importing a package that imports its own package is an import cycle. This bites in Task 1.
3. **Upload has two failure classes** (§5.2) and the whole point of the design is that they stay apart. Task 5's tests are what keep a later refactor from collapsing them back into one `MaxBytesReader`.

## File Structure

```
internal/service/storage/backend.go            Task 1: Backend, Entry, doc of the six semantics
internal/service/storage/local.go              Task 1: the os.Root implementation
internal/service/storage/backendtest/backendtest.go  Task 1: the conformance suite
internal/service/storage/local_test.go         Task 1: runs the suite (package storage_test)
internal/service/storage/storage.go            Task 2: Config, Service, clean(), Browse/Upload/…
internal/service/storage/storage_test.go       Task 2
internal/config/config.go                      Task 3: the Storage section
config.example.yaml                            Task 3
internal/app/services.go                       Task 3: the Storage field
commands/serve.go                              Task 3: Start block + public route
server/uploads_route_test.go                   Task 3: wildcard precedence
internal/controller/admin/filemanager.go       Task 4: mount, page, list; Task 5: mutations
internal/controller/admin/filemanager_test.go  Tasks 4, 5
internal/controller/admin/auth.go              Task 7: canBrowseFiles prop
frontend/src/components/admin/FileManager.vue       Task 6
frontend/src/components/admin/FileManager.test.ts   Task 6
frontend/pages/admin/filemanager/index.vue          Task 6
frontend/src/components/admin/FileManagerDialog.vue Task 7
frontend/src/components/admin/ImagePicker.vue       Task 7
frontend/src/components/admin/ImagePicker.test.ts   Task 7
internal/service/db/migrations/006_user_avatar.{up,down}.sql  Task 8
internal/controller/admin/user_crud.go              Task 8: avatar column + validation
frontend/pages/admin/user/{index,form}.vue          Task 8
server/ssr_admin_test.go                            Tasks 6, 8
cmd/goappctl/internal/components/components.go      Task 9
README.md                                           Task 9
```

---

### Task 1: The Backend contract, its conformance suite, and the local implementation

The suite comes first and is the test for the implementation. Six semantics
(§4.2), and semantic 4 gets three separate tests because it has three cases —
an implementation can pass one and fail the others in either direction.

**Files:**
- Create: `internal/service/storage/backend.go`, `internal/service/storage/local.go`, `internal/service/storage/backendtest/backendtest.go`, `internal/service/storage/local_test.go`

**Interfaces:**
- Consumes: nothing from this repo.
- Produces:
  ```go
  package storage
  type Entry struct { Name string; IsDir bool; Size int64; ModTime time.Time }
  type Backend interface {
      List(ctx context.Context, dir string) ([]Entry, error)
      Stat(ctx context.Context, name string) (Entry, error)
      Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
      Save(ctx context.Context, name string, r io.Reader) error
      Mkdir(ctx context.Context, dir string) error
      Rename(ctx context.Context, oldName, newName string) error
      Remove(ctx context.Context, name string) error
  }
  func NewLocalBackend(root *os.Root) Backend

  package backendtest
  func Run(t *testing.T, newBackend func(t *testing.T) storage.Backend)
  ```

- [ ] **Step 1: Write the conformance suite**

Create `internal/service/storage/backendtest/backendtest.go`. It is a normal
(non-`_test.go`) file so other packages can import it:

```go
// Package backendtest is the executable form of the Backend contract in
// §4.2 of the storage design spec. A backend that passes Run implements the
// interface; one that does not, does not — whatever its methods compile
// against. It exists because the interface has one implementation today, so
// prose is the only other place the semantics could live, and prose is not
// checkable.
package backendtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/millken/goapp-template/internal/service/storage"
)

// Run drives every contract case against a fresh backend per subtest.
func Run(t *testing.T, newBackend func(t *testing.T) storage.Backend) {
	t.Helper()
	ctx := context.Background()

	// Semantic 2: directories are real entities.
	t.Run("MkdirThenListShowsTheDirectory", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Mkdir(ctx, "photos"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		entries, err := b.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 1 || entries[0].Name != "photos" || !entries[0].IsDir {
			t.Fatalf("List(root) = %+v, want one IsDir entry named photos", entries)
		}
	})

	t.Run("ListOnAnEmptyDirectorySucceeds", func(t *testing.T) {
		// Not the same assertion as above: this is the caller opening a folder
		// they just created and getting an empty page rather than an error.
		b := newBackend(t)
		if err := b.Mkdir(ctx, "empty"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		entries, err := b.List(ctx, "empty")
		if err != nil {
			t.Fatalf("List(empty): %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("List(empty) = %+v, want none", entries)
		}
	})

	// Semantic 3: Save overwrites.
	t.Run("SaveOverwrites", func(t *testing.T) {
		b := newBackend(t)
		mustSave(t, b, "a.txt", "first")
		mustSave(t, b, "a.txt", "second")
		if got := mustRead(t, b, "a.txt"); got != "second" {
			t.Errorf("content = %q, want %q", got, "second")
		}
		entries, err := b.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("List = %+v, want a single entry", entries)
		}
	})

	// Semantic 4, case 1 of 3.
	t.Run("RemoveDeletesAFile", func(t *testing.T) {
		b := newBackend(t)
		mustSave(t, b, "a.txt", "x")
		if err := b.Remove(ctx, "a.txt"); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := b.Stat(ctx, "a.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat after Remove: err = %v, want fs.ErrNotExist", err)
		}
	})

	// Semantic 4, case 2 of 3 — the one a naive implementation fails in both
	// directions: os.Root.Remove passes here but fails case 3, and a
	// prefix-deleting object store may treat "no objects under this prefix" as
	// nothing to do and silently leave the directory listed.
	t.Run("RemoveDeletesAnEmptyDirectory", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Mkdir(ctx, "gone"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if err := b.Remove(ctx, "gone"); err != nil {
			t.Fatalf("Remove(empty dir): %v", err)
		}
		entries, err := b.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("List = %+v, want the directory gone", entries)
		}
	})

	// Semantic 4, case 3 of 3.
	t.Run("RemoveIsRecursive", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Mkdir(ctx, "tree"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		mustSave(t, b, "tree/a.txt", "x")
		if err := b.Remove(ctx, "tree"); err != nil {
			t.Fatalf("Remove(non-empty dir): %v", err)
		}
		if _, err := b.Stat(ctx, "tree"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(tree): err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("RemoveOnAMissingNameReportsNotExist", func(t *testing.T) {
		// A recursive delete that treats "already absent" as success would make
		// the API unable to tell a caller their path was wrong.
		b := newBackend(t)
		if err := b.Remove(ctx, "never-existed"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(missing): err = %v, want fs.ErrNotExist", err)
		}
	})

	// Semantic 5: Open seeks.
	t.Run("OpenReturnsASeekableReader", func(t *testing.T) {
		b := newBackend(t)
		mustSave(t, b, "a.txt", "0123456789")
		f, err := b.Open(ctx, "a.txt")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer f.Close()
		if _, err := f.Seek(4, io.SeekStart); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		rest, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(rest) != "456789" {
			t.Errorf("tail = %q, want %q", rest, "456789")
		}
		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			t.Fatalf("Seek(End): %v", err)
		}
		if size != 10 {
			t.Errorf("size via Seek = %d, want 10", size)
		}
	})

	// Semantic 6: error identity.
	t.Run("MissingPathsWrapErrNotExist", func(t *testing.T) {
		b := newBackend(t)
		if _, err := b.Open(ctx, "nope.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Open: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := b.List(ctx, "nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("List: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := b.Stat(ctx, "nope"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat: err = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("MkdirOverAnExistingNameWrapsErrExist", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Mkdir(ctx, "dup"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if err := b.Mkdir(ctx, "dup"); !errors.Is(err, fs.ErrExist) {
			t.Errorf("second Mkdir: err = %v, want fs.ErrExist", err)
		}
	})

	t.Run("RenameMovesAcrossDirectories", func(t *testing.T) {
		b := newBackend(t)
		if err := b.Mkdir(ctx, "dst"); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		mustSave(t, b, "a.txt", "x")
		if err := b.Rename(ctx, "a.txt", "dst/a.txt"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if got := mustRead(t, b, "dst/a.txt"); got != "x" {
			t.Errorf("content after move = %q", got)
		}
		if _, err := b.Stat(ctx, "a.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("source still present: err = %v", err)
		}
	})

	t.Run("StatReportsSizeAndKind", func(t *testing.T) {
		b := newBackend(t)
		mustSave(t, b, "a.txt", "12345")
		e, err := b.Stat(ctx, "a.txt")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if e.Name != "a.txt" || e.IsDir || e.Size != 5 {
			t.Errorf("Stat = %+v, want {a.txt false 5}", e)
		}
		if e.ModTime.IsZero() {
			t.Error("ModTime is zero")
		}
	})
}

func mustSave(t *testing.T, b storage.Backend, name, content string) {
	t.Helper()
	if err := b.Save(context.Background(), name, bytes.NewBufferString(content)); err != nil {
		t.Fatalf("Save(%s): %v", name, err)
	}
}

func mustRead(t *testing.T, b storage.Backend, name string) string {
	t.Helper()
	f, err := b.Open(context.Background(), name)
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", name, err)
	}
	return string(data)
}
```

- [ ] **Step 2: Write the test that runs the suite**

Create `internal/service/storage/local_test.go`. Note the package name: it is
`storage_test`, not `storage`. `backendtest` imports `storage`, so an internal
test file importing `backendtest` would be an import cycle.

```go
package storage_test

import (
	"os"
	"testing"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/goapp-template/internal/service/storage/backendtest"
)

func TestLocalBackend_Contract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) storage.Backend {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		t.Cleanup(func() { _ = root.Close() })
		return storage.NewLocalBackend(root)
	})
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/service/storage/... -count=1`
Expected: build failure — `undefined: storage.Backend`, `undefined: storage.NewLocalBackend`.

- [ ] **Step 4: Write the contract**

Create `internal/service/storage/backend.go`:

```go
// Package storage is the file-storage infrastructure service: a rooted tree of
// uploaded files behind a swappable Backend, with local disk as the only
// implementation. It implements app.Lifecycle — Start opens the root, Stop
// closes it — and imports no other component.
package storage

import (
	"context"
	"io"
	"time"
)

// Entry is one item in a directory listing.
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64 // 0 for directories
	ModTime time.Time
}

// Backend is the storage contract. Its semantics are numbered in §4.2 of the
// design spec and made executable by the backendtest package — a backend that
// has not been run against that suite has not implemented this interface.
//
// In brief, and normatively:
//
//  1. Paths are cleaned, slash-separated and relative; "" is the root. Service
//     guarantees this, so a backend must not re-derive it as security.
//  2. Directories are real entities: Mkdir then List shows them, even empty.
//  3. Save overwrites. Not-overwriting is Service's policy, not a backend's.
//  4. Remove deletes whatever is at name, recursively — a file, an empty
//     directory, or a populated one. A missing name is fs.ErrNotExist.
//  5. Open returns a ReadSeekCloser: http.ServeContent needs Seek.
//  6. No atomicity and no locking. Errors wrap fs.ErrNotExist / fs.ErrExist so
//     callers can map status codes with errors.Is and nothing else.
type Backend interface {
	List(ctx context.Context, dir string) ([]Entry, error)
	Stat(ctx context.Context, name string) (Entry, error)
	Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
	Save(ctx context.Context, name string, r io.Reader) error
	Mkdir(ctx context.Context, dir string) error
	Rename(ctx context.Context, oldName, newName string) error
	Remove(ctx context.Context, name string) error
}
```

- [ ] **Step 5: Write the local implementation**

Create `internal/service/storage/local.go`:

```go
package storage

import (
	"context"
	"io"
	"os"
)

// localBackend serves a tree through an *os.Root. Every operation goes through
// the root's methods, so a symlink pointing outside the tree fails in the
// kernel rather than in a string comparison we wrote.
type localBackend struct{ root *os.Root }

// NewLocalBackend wraps an open root. The caller owns the root's lifetime.
func NewLocalBackend(root *os.Root) Backend { return &localBackend{root: root} }

// osName maps the contract's root ("") to the one os understands (".").
func osName(name string) string {
	if name == "" {
		return "."
	}
	return name
}

func (b *localBackend) List(_ context.Context, dir string) ([]Entry, error) {
	f, err := b.root.Open(osName(dir))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			// Only one error is expected here: the entry was deleted between
			// ReadDir and Info, and a listing that reported it would be lying
			// about the present. Anything else — a permission or I/O failure —
			// is returned, because a listing that silently drops entries looks
			// exactly like a directory with fewer files in it.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, entryOf(de.Name(), info))
	}
	return out, nil
}

func (b *localBackend) Stat(_ context.Context, name string) (Entry, error) {
	info, err := b.root.Stat(osName(name))
	if err != nil {
		return Entry{}, err
	}
	return entryOf(info.Name(), info), nil
}

func (b *localBackend) Open(_ context.Context, name string) (io.ReadSeekCloser, error) {
	return b.root.Open(name)
}

func (b *localBackend) Save(_ context.Context, name string, r io.Reader) error {
	f, err := b.root.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (b *localBackend) Mkdir(_ context.Context, dir string) error {
	return b.root.Mkdir(dir, 0o755)
}

func (b *localBackend) Rename(_ context.Context, oldName, newName string) error {
	return b.root.Rename(oldName, newName)
}

func (b *localBackend) Remove(_ context.Context, name string) error {
	// Stat first: RemoveAll reports success for a path that was never there,
	// and semantic 6 requires fs.ErrNotExist so the API can answer 404.
	if _, err := b.root.Stat(osName(name)); err != nil {
		return err
	}
	return b.root.RemoveAll(name)
}

func entryOf(name string, info os.FileInfo) Entry {
	e := Entry{Name: name, IsDir: info.IsDir(), ModTime: info.ModTime()}
	if !e.IsDir {
		e.Size = info.Size()
	}
	return e
}
```

- [ ] **Step 6: Run the suite to verify it passes**

Run: `go test ./internal/service/storage/... -count=1 -v`
Expected: PASS, with all twelve subtests of `TestLocalBackend_Contract` listed.
If `RemoveDeletesAnEmptyDirectory` fails, `Remove` is calling `root.Remove`
instead of `root.RemoveAll` — that is the case this test exists for.

- [ ] **Step 7: Commit**

```bash
git add internal/service/storage
git commit -m "feat(storage): a Backend contract, executable, with one implementation"
```

---

### Task 2: The Service — cleaning, lifecycle, and policy

Everything the contract deliberately left out: which paths are legal, what a
listing page looks like, and what an upload is allowed to be.

**Files:**
- Create: `internal/service/storage/storage.go`, `internal/service/storage/storage_test.go`

**Interfaces:**
- Consumes: `Backend`, `Entry`, `NewLocalBackend` (Task 1).
- Produces:
  ```go
  type Config struct {
      Root string; URLPrefix string
      MaxUploadSize int64; MaxRequestSize int64; PageSize int
      AllowedExt []string
  }
  type Crumb struct { Name, Path string }
  type Listing struct { Path string; Breadcrumb []Crumb; Entries []Entry; Total, Page, PageSize int }
  type ItemError struct { Name, Reason string }
  var ErrBadPath = errors.New("storage: illegal path")
  var ErrRejected = errors.New("storage: rejected")   // upload policy
  func New(cfg *Config) *Service
  func (s *Service) Start(ctx context.Context) error
  func (s *Service) Stop(ctx context.Context) error
  func (s *Service) FS() fs.FS
  func (s *Service) URLPrefix() string
  func (s *Service) URLFor(name string) string
  func (s *Service) MaxRequestSize() int64
  func (s *Service) ValidatePath(name string) error
  func (s *Service) Browse(ctx context.Context, dir, query string, page int) (Listing, error)
  func (s *Service) Upload(ctx context.Context, dir, filename string, r io.Reader) (Entry, error)
  func (s *Service) Mkdir(ctx context.Context, dir, name string) error
  func (s *Service) Rename(ctx context.Context, name, newName string) error
  func (s *Service) Move(ctx context.Context, names []string, toDir string) ([]ItemError, error)
  func (s *Service) Delete(ctx context.Context, names []string) ([]ItemError, error)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/service/storage/storage_test.go` (internal package — it tests
`clean`, which is unexported):

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func startedService(t *testing.T) *Service {
	t.Helper()
	s := New(&Config{Root: t.TempDir()})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func TestClean_RejectsEverythingThatEscapesOrConfuses(t *testing.T) {
	bad := []string{
		"..", "../etc", "a/../../b", "/etc/passwd", `a\b`, "a/\x00b",
		"a//b", "a/./b", " ", strings.Repeat("x", 256),
	}
	for _, in := range bad {
		t.Run(fmt.Sprintf("%q", in), func(t *testing.T) {
			if _, err := clean(in); !errors.Is(err, ErrBadPath) {
				t.Errorf("clean(%q) err = %v, want ErrBadPath", in, err)
			}
		})
	}

	good := map[string]string{
		"":            "",
		"a.png":       "a.png",
		"photos":      "photos",
		"photos/a.png": "photos/a.png",
		"图片/一.png":   "图片/一.png",
	}
	for in, want := range good {
		if got, err := clean(in); err != nil || got != want {
			t.Errorf("clean(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
}

func TestStart_CreatesTheRootAndRejectsAnEmptyOne(t *testing.T) {
	if err := New(&Config{}).Start(context.Background()); err == nil {
		t.Error("an empty root must be refused, not defaulted")
	}
	if err := New(nil).Start(context.Background()); err == nil {
		t.Error("a missing [storage] section must be refused")
	}
	s := startedService(t)
	if s.URLPrefix() != "/uploads" {
		t.Errorf("URLPrefix default = %q", s.URLPrefix())
	}
}

func TestUpload_EnforcesExtensionSizeAndCollisions(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)

	if _, err := s.Upload(ctx, "", "evil.exe", strings.NewReader("x")); !errors.Is(err, ErrRejected) {
		t.Errorf("an unlisted extension must be rejected, got %v", err)
	}

	s.cfg.MaxUploadSize = 4
	if _, err := s.Upload(ctx, "", "big.png", strings.NewReader("12345")); !errors.Is(err, ErrRejected) {
		t.Errorf("over the cap must be rejected, got %v", err)
	}
	if _, err := s.Stat(ctx, "big.png"); err == nil {
		t.Error("a rejected upload must not leave the partial file behind")
	}

	s.cfg.MaxUploadSize = 1 << 20
	first, err := s.Upload(ctx, "", "a.png", strings.NewReader("one"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	second, err := s.Upload(ctx, "", "a.png", strings.NewReader("two"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if first.Name != "a.png" || second.Name != "a-2.png" {
		t.Errorf("collision: got %q then %q, want a.png then a-2.png", first.Name, second.Name)
	}

	// A client filename is a hostile string, not a path.
	e, err := s.Upload(ctx, "", "../../x.png", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if e.Name != "x.png" {
		t.Errorf("sanitised name = %q, want x.png", e.Name)
	}
}

func TestBrowse_SortsFiltersAndPaginates(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	s.cfg.PageSize = 2

	if err := s.Mkdir(ctx, "", "zebra"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Beta.png", "alpha.png", "gamma.png"} {
		if _, err := s.Upload(ctx, "", n, strings.NewReader("x")); err != nil {
			t.Fatal(err)
		}
	}

	page1, err := s.Browse(ctx, "", "", 1)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if page1.Total != 4 || len(page1.Entries) != 2 {
		t.Fatalf("page 1: total = %d, entries = %d; want 4 and 2", page1.Total, len(page1.Entries))
	}
	if page1.Entries[0].Name != "zebra" || !page1.Entries[0].IsDir {
		t.Errorf("directories must sort first, got %q", page1.Entries[0].Name)
	}
	if page1.Entries[1].Name != "alpha.png" {
		t.Errorf("case-insensitive name sort: got %q, want alpha.png", page1.Entries[1].Name)
	}

	page2, err := s.Browse(ctx, "", "", 2)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(page2.Entries) != 2 || page2.Entries[0].Name != "Beta.png" {
		t.Errorf("page 2 = %+v", page2.Entries)
	}

	filtered, err := s.Browse(ctx, "", "AM", 1)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if filtered.Total != 1 || filtered.Entries[0].Name != "gamma.png" {
		t.Errorf("filter: total = %d, entries = %+v", filtered.Total, filtered.Entries)
	}
}

func TestBrowse_BuildsABreadcrumb(t *testing.T) {
	s := startedService(t)
	ctx := context.Background()
	if err := s.Mkdir(ctx, "", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}
	l, err := s.Browse(ctx, "a/b", "", 1)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	want := []Crumb{{Name: "全部文件", Path: ""}, {Name: "a", Path: "a"}, {Name: "b", Path: "a/b"}}
	if len(l.Breadcrumb) != len(want) {
		t.Fatalf("breadcrumb = %+v, want %+v", l.Breadcrumb, want)
	}
	for i := range want {
		if l.Breadcrumb[i] != want[i] {
			t.Errorf("crumb %d = %+v, want %+v", i, l.Breadcrumb[i], want[i])
		}
	}
}

func TestDeleteAndMove_ReportPerItemFailures(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, "", "dst"); err != nil {
		t.Fatal(err)
	}

	fails, err := s.Delete(ctx, []string{"a.png", "missing.png"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fails) != 1 || fails[0].Name != "missing.png" {
		t.Errorf("per-item failures = %+v, want only missing.png", fails)
	}

	if _, err := s.Upload(ctx, "", "b.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	fails, err = s.Move(ctx, []string{"b.png", "missing.png"}, "dst")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(fails) != 1 {
		t.Errorf("per-item failures = %+v", fails)
	}
	if _, err := s.Stat(ctx, "dst/b.png"); err != nil {
		t.Errorf("the movable item must still have moved: %v", err)
	}
}

func TestDelete_RefusesTheRoot(t *testing.T) {
	s := startedService(t)
	fails, err := s.Delete(context.Background(), []string{""})
	if err == nil && len(fails) == 0 {
		t.Fatal("deleting the root must not be allowed")
	}
}

func TestValidatePath_IsTheExportedGate(t *testing.T) {
	s := startedService(t)
	if err := s.ValidatePath(""); err != nil {
		t.Errorf("an empty avatar means no avatar and must pass: %v", err)
	}
	if err := s.ValidatePath("a/b.png"); err != nil {
		t.Errorf("a legal path must pass: %v", err)
	}
	if err := s.ValidatePath("../secret"); !errors.Is(err, ErrBadPath) {
		t.Errorf("err = %v, want ErrBadPath", err)
	}
}

func TestURLFor(t *testing.T) {
	s := startedService(t)
	if got := s.URLFor("a/b.png"); got != "/uploads/a/b.png" {
		t.Errorf("URLFor = %q", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/service/storage/ -count=1`
Expected: build failure — `undefined: New`, `undefined: clean`, `undefined: Config`.

- [ ] **Step 3: Write the implementation**

Create `internal/service/storage/storage.go`:

```go
package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Sentinels the HTTP layer maps to status codes. ErrBadPath is a malformed or
// escaping path (422); ErrRejected is an upload the policy refuses (422).
// Everything else arrives from a Backend already wrapping fs.ErrNotExist or
// fs.ErrExist.
var (
	ErrBadPath  = errors.New("storage: illegal path")
	ErrRejected = errors.New("storage: rejected")
)

// Defaults applied by the accessors when a field is left empty.
const (
	defaultURLPrefix     = "/uploads"
	defaultMaxUploadSize = 8 << 20  // 8MB per file
	defaultMaxRequestSize = 64 << 20 // 64MB per upload request
	defaultPageSize      = 40
	// rootCrumbLabel names the root in a breadcrumb; "" would render blank.
	rootCrumbLabel = "全部文件"
)

// defaultAllowedExt deliberately omits .svg: an SVG served from our own origin
// executes script on our own origin. An operator who needs it can list it.
var defaultAllowedExt = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".zip"}

// Config configures the storage service. A pointer in New lets Start
// distinguish "enabled but misconfigured" (nil) from "not enabled".
type Config struct {
	// Root is the browsable tree. Required: every plausible default is a
	// directory the app would then start writing user uploads into by surprise.
	Root string `yaml:"root"`
	// URLPrefix is the public read path (default "/uploads").
	URLPrefix string `yaml:"url_prefix"`
	// MaxUploadSize caps one file (default 8MB).
	MaxUploadSize int64 `yaml:"max_upload_size"`
	// MaxRequestSize caps a whole upload request (default 64MB). Distinct from
	// MaxUploadSize on purpose — see §5.2 of the spec: one is a per-item
	// rejection, the other kills the request.
	MaxRequestSize int64 `yaml:"max_request_size"`
	// PageSize is entries per listing page (default 40).
	PageSize int `yaml:"page_size"`
	// AllowedExt is the upload whitelist, lowercase with dots.
	AllowedExt []string `yaml:"allowed_ext"`
}

// Crumb is one breadcrumb segment.
type Crumb struct {
	Name string
	Path string
}

// Listing is one page of a directory.
type Listing struct {
	Path       string
	Breadcrumb []Crumb
	Entries    []Entry
	Total      int
	Page       int
	PageSize   int
}

// ItemError is one item's failure inside a batch operation.
type ItemError struct {
	Name   string
	Reason string
}

// Service is the storage infrastructure service.
type Service struct {
	cfg  *Config
	root *os.Root
	be   Backend
}

// New constructs the service. Nothing is opened until Start.
func New(cfg *Config) *Service { return &Service{cfg: cfg} }

// Start resolves and creates the root, opens it, and proves it is writable.
func (s *Service) Start(_ context.Context) error {
	if s.cfg == nil {
		return errors.New("storage: service enabled but [storage] config section missing")
	}
	if strings.TrimSpace(s.cfg.Root) == "" {
		return errors.New("storage: root is required")
	}
	abs, err := filepath.Abs(s.cfg.Root)
	if err != nil {
		return fmt.Errorf("storage: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("storage: create root: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return fmt.Errorf("storage: open root: %w", err)
	}
	// Write probe: a read-only uploads directory is a startup problem, and
	// finding it here beats finding it on a user's first upload.
	const probe = ".storage-write-probe"
	f, err := root.Create(probe)
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("storage: root is not writable: %w", err)
	}
	_ = f.Close()
	_ = root.Remove(probe)

	s.root = root
	s.be = NewLocalBackend(root)
	return nil
}

// Stop closes the root.
func (s *Service) Stop(_ context.Context) error {
	if s.root == nil {
		return nil
	}
	return s.root.Close()
}

// FS exposes the tree for the public static route. It is root.FS(), not
// os.DirFS: the latter would follow a symlink planted inside the tree out of it.
func (s *Service) FS() fs.FS { return s.root.FS() }

func (s *Service) URLPrefix() string { return cmp.Or(s.cfg.URLPrefix, defaultURLPrefix) }

// URLFor is the public URL of a stored path.
func (s *Service) URLFor(name string) string { return s.URLPrefix() + "/" + name }

func (s *Service) MaxRequestSize() int64 {
	return cmp.Or(s.cfg.MaxRequestSize, int64(defaultMaxRequestSize))
}

func (s *Service) maxUploadSize() int64 {
	return cmp.Or(s.cfg.MaxUploadSize, int64(defaultMaxUploadSize))
}

func (s *Service) pageSize() int { return cmp.Or(s.cfg.PageSize, defaultPageSize) }

func (s *Service) allowedExt() []string {
	if len(s.cfg.AllowedExt) == 0 {
		return defaultAllowedExt
	}
	return s.cfg.AllowedExt
}

// ValidatePath is clean with the cleaned value discarded: the exported way for
// another package to ask "is this a legal path inside the tree?" without clean
// itself becoming API. An empty path is legal and means "nothing selected".
func (s *Service) ValidatePath(name string) error {
	_, err := clean(name)
	return err
}

// clean is the single gate every public method passes its input through. It
// returns a slash-separated relative path, or ErrBadPath.
//
// It is strict rather than forgiving on purpose: a path that needs
// interpretation is a path a future reader will interpret differently. Note
// that os.Root would also refuse an escape — this is the first of two layers,
// not the only one.
func clean(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.ContainsAny(p, `\`) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: control character", ErrBadPath)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "", seg == ".", seg == "..":
			return "", fmt.Errorf("%w: %q", ErrBadPath, p)
		case len(seg) > 255:
			return "", fmt.Errorf("%w: segment too long", ErrBadPath)
		case strings.TrimSpace(seg) == "":
			return "", fmt.Errorf("%w: blank segment", ErrBadPath)
		}
	}
	return p, nil
}

// join cleans a directory and a base name into one path.
func join(dir, name string) (string, error) {
	d, err := clean(dir)
	if err != nil {
		return "", err
	}
	if _, err := clean(name); err != nil {
		return "", err
	}
	if d == "" {
		return name, nil
	}
	return d + "/" + name, nil
}

// Browse returns one page of dir, filtered and sorted.
func (s *Service) Browse(ctx context.Context, dir, query string, page int) (Listing, error) {
	d, err := clean(dir)
	if err != nil {
		return Listing{}, err
	}
	entries, err := s.be.List(ctx, d)
	if err != nil {
		return Listing{}, err
	}

	if q := strings.ToLower(strings.TrimSpace(query)); q != "" {
		entries = slices.DeleteFunc(entries, func(e Entry) bool {
			return !strings.Contains(strings.ToLower(e.Name), q)
		})
	}
	slices.SortFunc(entries, func(x, y Entry) int {
		if x.IsDir != y.IsDir {
			if x.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})

	size := s.pageSize()
	if page < 1 {
		page = 1
	}
	total := len(entries)
	start := min((page-1)*size, total)
	out := entries[start:min(start+size, total)]

	return Listing{
		Path:       d,
		Breadcrumb: breadcrumb(d),
		Entries:    out,
		Total:      total,
		Page:       page,
		PageSize:   size,
	}, nil
}

// breadcrumb turns "a/b" into root, a, a/b.
func breadcrumb(dir string) []Crumb {
	out := []Crumb{{Name: rootCrumbLabel, Path: ""}}
	if dir == "" {
		return out
	}
	acc := ""
	for _, seg := range strings.Split(dir, "/") {
		if acc == "" {
			acc = seg
		} else {
			acc += "/" + seg
		}
		out = append(out, Crumb{Name: seg, Path: acc})
	}
	return out
}

// Stat reports one entry, for callers that need to know a path exists.
func (s *Service) Stat(ctx context.Context, name string) (Entry, error) {
	n, err := clean(name)
	if err != nil {
		return Entry{}, err
	}
	return s.be.Stat(ctx, n)
}

// Upload stores one file under dir, applying the whole policy: extension
// whitelist, filename sanitisation, collision renaming, and the per-file size
// cap. r is read at most MaxUploadSize+1 bytes; exceeding that removes what was
// written and returns ErrRejected, because a half-file is worse than no file.
func (s *Service) Upload(ctx context.Context, dir, filename string, r io.Reader) (Entry, error) {
	d, err := clean(dir)
	if err != nil {
		return Entry{}, err
	}
	name := sanitiseFilename(filename)
	if name == "" {
		return Entry{}, fmt.Errorf("%w: 文件名为空", ErrRejected)
	}
	ext := strings.ToLower(path.Ext(name))
	if !slices.Contains(s.allowedExt(), ext) {
		return Entry{}, fmt.Errorf("%w: 不接受的文件类型 %q", ErrRejected, ext)
	}

	name, err = s.freeName(ctx, d, name)
	if err != nil {
		return Entry{}, err
	}
	full, err := join(d, name)
	if err != nil {
		return Entry{}, err
	}

	max := s.maxUploadSize()
	limited := &io.LimitedReader{R: r, N: max + 1}
	if err := s.be.Save(ctx, full, limited); err != nil {
		return Entry{}, err
	}
	if limited.N == 0 { // read max+1 bytes: the file is over the cap
		_ = s.be.Remove(ctx, full)
		return Entry{}, fmt.Errorf("%w: 文件超过 %d 字节", ErrRejected, max)
	}
	return s.be.Stat(ctx, full)
}

// freeName returns name, or name-2/name-3… when taken.
func (s *Service) freeName(ctx context.Context, dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		full, err := join(dir, candidate)
		if err != nil {
			return "", err
		}
		if _, err := s.be.Stat(ctx, full); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

// sanitiseFilename reduces a client-supplied name to a safe base name. Unicode
// letters survive — a Chinese filename is legitimate — but separators, control
// characters and leading dots do not.
func sanitiseFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r):
			// dropped
		case unicode.IsSpace(r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".-")
}

// Mkdir creates one directory inside dir.
func (s *Service) Mkdir(ctx context.Context, dir, name string) error {
	if sanitiseFilename(name) != name || name == "" {
		return fmt.Errorf("%w: 目录名不合法", ErrBadPath)
	}
	full, err := join(dir, name)
	if err != nil {
		return err
	}
	return s.be.Mkdir(ctx, full)
}

// Rename renames one entry within its own directory. newName is a base name:
// accepting a path here would make "rename" a silent move.
func (s *Service) Rename(ctx context.Context, name, newName string) error {
	n, err := clean(name)
	if err != nil {
		return err
	}
	if n == "" {
		return fmt.Errorf("%w: 不能重命名根目录", ErrBadPath)
	}
	if sanitiseFilename(newName) != newName || newName == "" {
		return fmt.Errorf("%w: 新名称不合法", ErrBadPath)
	}
	// path.Dir("a.png") is ".": this package spells the root "", and join
	// would otherwise reject "." as an illegal segment. Decide the directory
	// before building the target, not after.
	dir := path.Dir(strings.TrimSuffix(n, "/"))
	if dir == "." {
		dir = ""
	}
	target, err := join(dir, newName)
	if err != nil {
		return err
	}
	return s.be.Rename(ctx, n, target)
}

// Move relocates entries into toDir, reporting per-item failures rather than
// stopping at the first. The returned error is a whole-request failure.
func (s *Service) Move(ctx context.Context, names []string, toDir string) ([]ItemError, error) {
	dst, err := clean(toDir)
	if err != nil {
		return nil, err
	}
	if dst != "" {
		if e, err := s.be.Stat(ctx, dst); err != nil {
			return nil, err
		} else if !e.IsDir {
			return nil, fmt.Errorf("%w: 目标不是目录", ErrBadPath)
		}
	}

	var fails []ItemError
	for _, raw := range names {
		n, err := clean(raw)
		if err != nil || n == "" {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		target, err := join(dst, path.Base(n))
		if err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		if err := s.be.Rename(ctx, n, target); err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: reason(err)})
		}
	}
	return fails, nil
}

// Delete removes entries, reporting per-item failures. The root is refused.
func (s *Service) Delete(ctx context.Context, names []string) ([]ItemError, error) {
	var fails []ItemError
	for _, raw := range names {
		n, err := clean(raw)
		if err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		if n == "" {
			fails = append(fails, ItemError{Name: raw, Reason: "不能删除根目录"})
			continue
		}
		if err := s.be.Remove(ctx, n); err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: reason(err)})
		}
	}
	return fails, nil
}

// reason turns a backend error into a message safe to hand a browser.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "不存在"
	case errors.Is(err, fs.ErrExist):
		return "同名项已存在"
	default:
		return "操作失败"
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/service/storage/... -count=1`
Expected: PASS, including Task 1's contract suite (unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/service/storage
git commit -m "feat(storage): the service — one gate for paths, one policy for uploads"
```

---

### Task 3: Config, composition root, and the public read route

After this task an operator can configure `[storage]`, start the app, and read
a file they dropped into the uploads directory over HTTP — in both dev and prod.

**Files:**
- Modify: `internal/config/config.go`, `config.example.yaml`, `internal/app/services.go`, `commands/serve.go`
- Create: `server/uploads_route_test.go`

**Interfaces:**
- Consumes: `storage.New`, `Start`, `Stop`, `FS`, `URLPrefix` (Task 2).
- Produces: `app.Services.Storage *storage.Service`; `config.Config.Storage *storage.Config`.

- [ ] **Step 1: Write the failing test**

Create `server/uploads_route_test.go`. This pins §5.1's claim — the assertion
the whole public-read design rests on:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/millken/inertia"
)

// TestUploadsRouteBeatsTheDistWildcard pins route precedence: serve.go registers
// GET /uploads/* while server.New has already registered GET /* for dist, and
// the uploads route must win without depending on which was registered first.
// If this ever fails, every uploaded file is being served from dist's FS (404)
// and the fix is a router problem, not a storage one.
func TestUploadsRouteBeatsTheDistWildcard(t *testing.T) {
	dist := fstest.MapFS{"assets/main.js": {Data: []byte("dist")}}
	uploads := fstest.MapFS{"a/b.png": {Data: []byte("upload")}}

	for _, name := range []string{"dist-first", "uploads-first"} {
		t.Run(name, func(t *testing.T) {
			eng, err := inertia.New(inertia.WithMode(inertia.ModeProduction))
			if err != nil {
				t.Fatalf("inertia.New: %v", err)
			}
			register := map[string]func(){
				"dist":    func() { eng.StaticFS("/", dist) },
				"uploads": func() { eng.GET("/uploads/*", inertia.StaticFileServer("/uploads", uploads)) },
			}
			order := []string{"dist", "uploads"}
			if name == "uploads-first" {
				order = []string{"uploads", "dist"}
			}
			for _, k := range order {
				register[k]()
			}
			if err := eng.RegistrationError(); err != nil {
				t.Fatalf("registration: %v", err)
			}

			for path, want := range map[string]string{
				"/uploads/a/b.png": "upload",
				"/assets/main.js":  "dist",
			} {
				w := httptest.NewRecorder()
				eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("GET %s = %d, want 200", path, w.Code)
				}
				if got := w.Body.String(); got != want {
					t.Errorf("GET %s served %q, want %q", path, got, want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails or passes for the right reason**

Run: `go test ./server/ -run TestUploadsRouteBeatsTheDistWildcard -count=1 -v`
Expected: PASS. This one is a characterisation test — it passes immediately
because the router already behaves this way, and its job is to fail if that ever
changes. Confirm it is genuinely exercising the code by temporarily changing
`"/uploads/*"` to `"/other/*"` and seeing `GET /uploads/a/b.png = 404`; then
change it back.

- [ ] **Step 3: Add the config section**

In `internal/config/config.go`, add the import and the field:

```go
	//goappctl:storage
	"github.com/millken/goapp-template/internal/service/storage"
	//goappctl:end
```

and inside `type Config struct`, after the `Admin` block:

```go
	//goappctl:storage
	// Storage holds the file-storage service config. nil when not used.
	Storage *storage.Config `yaml:"storage"`
	//goappctl:end
```

Leave `defaults()` alone: component sections have no defaults, which is what
makes "enabled but unconfigured" an error instead of a silent fallback.

- [ ] **Step 4: Add the container field**

In `internal/app/services.go`, add the import inside a marker and the field
after `Session`:

```go
	//goappctl:storage
	// Storage is the uploaded-file tree. Same rule as Session: its type comes
	// from an optional component, so only the storage and admin areas may
	// reference it.
	Storage *storage.Service
	//goappctl:end
```

- [ ] **Step 5: Wire the composition root**

In `commands/serve.go`, after the session block:

```go
	//goappctl:storage
	// Independent of db and session: it owns a directory, nothing else.
	storSvc := storage.New(cfg.Storage)
	if err := storSvc.Start(cmd.Context()); err != nil {
		return fmt.Errorf("start storage: %w", err)
	}
	defer func() { _ = storSvc.Stop(context.Background()) }()
	svc.Storage = storSvc
	//goappctl:end
```

and after `server.New`, next to the middleware wiring:

```go
	//goappctl:storage
	// Not eng.StaticFS: that helper is a no-op in development mode, where dist
	// is Vite's job — but uploads must be readable in both modes.
	eng.GET(storSvc.URLPrefix()+"/*",
		inertia.StaticFileServer(storSvc.URLPrefix(), storSvc.FS()))
	//goappctl:end
```

Add `"github.com/millken/inertia"` and the storage package to the imports (the
storage import goes inside a `//goappctl:storage` marker block).

- [ ] **Step 6: Document the section**

In `config.example.yaml`, after the `admin` block:

```yaml
#goappctl:storage
storage:
  root: uploads # REQUIRED — the browsable upload tree (no default: every
  # plausible one is a directory the app would start writing into by surprise)
  url_prefix: /uploads # public read path; files are world-readable by design
  # max_upload_size: 8388608     # bytes per file (default 8MB)
  # max_request_size: 67108864   # bytes per upload request (default 64MB)
  # page_size: 40                # entries per page in the file manager
  # Upload whitelist. .svg is deliberately absent: an SVG served from this
  # origin executes script on this origin. Add it only if you understand that.
  # allowed_ext: [".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".zip"]
#goappctl:end
```

- [ ] **Step 7: Verify the whole thing builds and runs**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all pass.

Then prove the route end to end:

```bash
mkdir -p uploads && echo hello > uploads/probe.txt
printf '\nstorage:\n  root: uploads\n' >> config.yaml   # if config.yaml exists
MYAPP_HOME=. go run . serve -a :8099 &
sleep 2 && curl -s http://localhost:8099/uploads/probe.txt && kill %1
```
Expected: `hello`. Then `rm -rf uploads` and revert the `config.yaml` edit.

- [ ] **Step 8: Commit**

```bash
git add internal/config internal/app commands config.example.yaml server/uploads_route_test.go
git commit -m "feat(storage): configure it, start it, and serve the tree"
```

---

### Task 4: The admin area — mount and list

The page route and the read endpoint. Mutations are Task 5, so this task can be
reviewed on its permissions and JSON shape alone.

**Files:**
- Create: `internal/controller/admin/filemanager.go`, `internal/controller/admin/filemanager_test.go`
- Modify: `internal/controller/admin/admin.go` (one call in `Mount`)

**Interfaces:**
- Consumes: `Registrar` (`a.Resource`), `a.Storage` from the embedded `*app.Services`, `storage.Listing/Entry/ErrBadPath/ErrRejected`.
- Produces:
  ```go
  func (a *Admin) mountFileManager(eng *inertia.Engine)
  func (a *Admin) fileManagerBase() string
  func (a *Admin) fmFail(c *inertia.Context, err error)  // used by Task 5
  type fmEntry struct{…}                                  // the JSON DTO
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/filemanager_test.go`:

```go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// fmStack is adminStack plus a started storage service on a temp directory.
// The admin area reads svc.Storage, so it has to be filled before Mount.
func fmStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	stor := storage.New(&storage.Config{Root: t.TempDir()})
	if err := stor.Start(context.Background()); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(context.Background()) })
	adm.Storage = stor
	adm.mountFileManager(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

// getJSON drives an authenticated GET and decodes the JSON body.
func getJSON(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	var body map[string]any
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v; body: %s", path, err, w.Body.String())
		}
	}
	return w.Code, body
}

func TestFileManagerList_ReturnsEntriesWithPathsAndURLs(t *testing.T) {
	eng, adm, cookie := fmStack(t)
	ctx := context.Background()
	if err := adm.Storage.Mkdir(ctx, "", "photos"); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Storage.Upload(ctx, "photos", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}

	code, body := getJSON(t, eng, cookie, "/admin/filemanager/api/list?path=photos")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v", body["entries"])
	}
	e, _ := entries[0].(map[string]any)
	if e["name"] != "a.png" || e["path"] != "photos/a.png" || e["url"] != "/uploads/photos/a.png" {
		t.Errorf("entry = %+v", e)
	}
	if e["dir"] != false {
		t.Errorf("dir = %v, want false", e["dir"])
	}
	if body["total"].(float64) != 1 {
		t.Errorf("total = %v", body["total"])
	}
	crumbs, _ := body["breadcrumb"].([]any)
	if len(crumbs) != 2 {
		t.Errorf("breadcrumb = %v", body["breadcrumb"])
	}
}

func TestFileManagerList_RejectsAnEscapingPath(t *testing.T) {
	eng, _, cookie := fmStack(t)
	code, body := getJSON(t, eng, cookie, "/admin/filemanager/api/list?path=../../etc")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	if body["error"] == nil {
		t.Error("a failure must carry an error message")
	}
}

func TestFileManagerList_MissingDirectoryIs404(t *testing.T) {
	eng, _, cookie := fmStack(t)
	code, _ := getJSON(t, eng, cookie, "/admin/filemanager/api/list?path=nope")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestFileManager_PermissionKeysAreRegistered(t *testing.T) {
	_, adm, _ := fmStack(t)
	var access, modify bool
	for _, p := range adm.Permissions() {
		switch p.Key {
		case "filemanager.access":
			access = true
		case "filemanager.modify":
			modify = true
		}
	}
	if !access || !modify {
		t.Errorf("catalogue = %+v, want both filemanager keys", adm.Permissions())
	}
}

func TestFileManager_AGroupWithoutAccessIsRefused(t *testing.T) {
	eng, adm, _ := fmStack(t)
	ctx := context.Background()
	// A group holding nothing, and a user in it.
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Nobody', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, group_id)
		 VALUES ('nobody', ?, 0, (SELECT id FROM user_groups WHERE name = 'Nobody'))`, hash); err != nil {
		t.Fatal(err)
	}
	cookie := loginAs(t, eng, "nobody", "pw")

	code, _ := getJSON(t, eng, cookie, "/admin/filemanager/api/list")
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
}
```

If `loginAs` does not exist in the admin test files, add it next to
`loginAndGetCookie` in `login_test.go`:

```go
// loginAs signs in as an arbitrary seeded user and returns the session cookie.
func loginAs(t *testing.T, eng *inertia.Engine, username, password string) *http.Cookie {
	t.Helper()
	w := postForm(t, eng, "/admin/login", url.Values{
		"username": {username}, "password": {password},
	})
	cs := w.Result().Cookies()
	if len(cs) == 0 {
		t.Fatalf("login as %s set no cookie (status %d)", username, w.Code)
	}
	return &http.Cookie{Name: cs[0].Name, Value: cs[0].Value}
}
```

Check `login_test.go` first — reuse `loginAndGetCookie`'s existing shape rather
than inventing a second style, and if a helper with this behaviour is already
there under another name, use that one and drop this.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/controller/admin/ -run TestFileManager -count=1`
Expected: build failure — `adm.mountFileManager undefined`.

- [ ] **Step 3: Write the mount, the page, and list**

Create `internal/controller/admin/filemanager.go`:

```go
package admin

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// mountFileManager registers the media library. Everything goes through the
// registrar, so filemanager.access guards the reads and filemanager.modify the
// writes without either being named here, and the sidebar entry is gated by the
// same key. The section is the registrar's default, "内容": a media library is
// content, not access control.
func (a *Admin) mountFileManager(eng *inertia.Engine) {
	base := a.fileManagerBase()
	r := a.Resource(eng, "filemanager")

	r.GET(base, a.fileManagerPage)
	r.GET(base+"/api/list", a.fmList)
	r.POST(base+"/api/upload", a.fmUpload)
	r.POST(base+"/api/mkdir", a.fmMkdir)
	r.POST(base+"/api/rename", a.fmRename)
	r.POST(base+"/api/move", a.fmMove)
	r.POST(base+"/api/delete", a.fmDelete)
	r.Menu("内容", "文件", base)
}

func (a *Admin) fileManagerBase() string { return a.Prefix() + "/filemanager" }

// fmEntry is the JSON shape of one listing row. It is a DTO rather than
// storage.Entry so the browser gets what it actually needs — a full path and a
// URL — without those becoming part of the storage contract.
type fmEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	URL   string `json:"url"`
}

// fileManagerPage renders the standalone library. The listing itself arrives
// over the JSON API, so this handler ships only what the component needs to
// start talking to it — which is also why it renders under SSR with no data.
func (a *Admin) fileManagerPage(c *inertia.Context) {
	c.Set("basePath", a.fileManagerBase())
	c.Set("urlPrefix", a.Storage.URLPrefix())
	if err := c.Render("admin/filemanager/index"); err != nil {
		slog.Error("render admin filemanager", "err", err)
	}
}

// fmList answers one page of a directory.
func (a *Admin) fmList(c *inertia.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	listing, err := a.Storage.Browse(c.Request.Context(), c.Query("path"), c.Query("q"), page)
	if err != nil {
		a.fmFail(c, err)
		return
	}

	entries := make([]fmEntry, 0, len(listing.Entries))
	for _, e := range listing.Entries {
		full := e.Name
		if listing.Path != "" {
			full = listing.Path + "/" + e.Name
		}
		entries = append(entries, fmEntry{
			Name: e.Name, Path: full, Dir: e.IsDir, Size: e.Size,
			MTime: e.ModTime.Unix(), URL: a.Storage.URLFor(full),
		})
	}

	crumbs := make([]map[string]string, 0, len(listing.Breadcrumb))
	for _, b := range listing.Breadcrumb {
		crumbs = append(crumbs, map[string]string{"name": b.Name, "path": b.Path})
	}

	a.fmOK(c, map[string]any{
		"path":       listing.Path,
		"breadcrumb": crumbs,
		"entries":    entries,
		"total":      listing.Total,
		"page":       listing.Page,
		"pageSize":   listing.PageSize,
	})
}

// fmOK writes a success body with ok:true merged in.
func (a *Admin) fmOK(c *inertia.Context, body map[string]any) {
	body["ok"] = true
	if err := c.JSON(body); err != nil {
		slog.Error("filemanager: write json", "err", err)
	}
}

// fmFail maps a storage error to a status and a message. The mapping lives in
// one place so every endpoint answers the same way, and so a new endpoint
// cannot invent its own vocabulary.
func (a *Admin) fmFail(c *inertia.Context, err error) {
	status, msg := http.StatusInternalServerError, "服务器错误"
	switch {
	case errors.Is(err, storage.ErrBadPath), errors.Is(err, storage.ErrRejected):
		status, msg = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, fs.ErrNotExist):
		status, msg = http.StatusNotFound, "不存在"
	case errors.Is(err, fs.ErrExist):
		status, msg = http.StatusConflict, "同名项已存在"
	default:
		// Logged, not returned: the detail may name a filesystem path.
		slog.Error("filemanager", "err", err, "path", c.Request.URL.Path)
	}
	c.Status(status)
	if err := c.JSON(map[string]string{"error": msg}); err != nil {
		slog.Error("filemanager: write error json", "err", err)
	}
}
```

Task 5 adds `fmUpload`, `fmMkdir`, `fmRename`, `fmMove` and `fmDelete`. To keep
this task compiling on its own, add them now as stubs that Task 5 replaces:

```go
// Replaced in full by Task 5.
func (a *Admin) fmUpload(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmMkdir(c *inertia.Context)  { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmRename(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmMove(c *inertia.Context)   { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmDelete(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
```

- [ ] **Step 4: Call it from Mount**

In `internal/controller/admin/admin.go`, inside `Mount`, after `a.mountAccount(eng)`:

```go
	//goappctl:storage
	a.mountFileManager(eng)
	//goappctl:end
```

Update `Mount`'s doc comment to mention the file manager alongside the user and
group resources — the comment enumerates what it registers, and a stale
enumeration is worse than none.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/controller/admin/ -run TestFileManager -count=1 -v`
Expected: PASS, five tests.

- [ ] **Step 6: Run the whole suite**

Run: `go test ./... -count=1`
Expected: PASS. `loginStack` now mounts a route that reads `a.Storage`, which is
nil in every pre-existing admin test — this is why `mountFileManager` is called
from the test helper rather than from `Mount` in those tests. If any existing
test panics with a nil dereference, the mount is happening where it should not.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/admin
git commit -m "feat(admin): a file manager area, its permission keys, and a listing"
```

---

### Task 5: The mutations, and the two upload failure classes

**Files:**
- Modify: `internal/controller/admin/filemanager.go` (replace the five stubs)
- Modify: `internal/controller/admin/filemanager_test.go`

**Interfaces:**
- Consumes: `a.fmOK`, `a.fmFail`, `fmEntry` (Task 4); `Storage.Upload/Mkdir/Rename/Move/Delete/MaxRequestSize` (Task 2).
- Produces: the JSON request shapes the frontend uses in Tasks 6–7:
  ```
  POST api/upload?path=<dir>   multipart, files under the field name "files"
  POST api/mkdir   {"path":"<dir>","name":"<new>"}
  POST api/rename  {"path":"<entry>","name":"<new base name>"}
  POST api/move    {"paths":["…"],"to":"<dir>"}
  POST api/delete  {"paths":["…"]}
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/controller/admin/filemanager_test.go`:

```go
// postJSON drives an authenticated JSON POST carrying the CSRF header, which is
// how the real client sends it — and, per csrf.go, is also what leaves the body
// unread so a multipart upload can stream.
func postJSON(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path, body string) (int, map[string]any) {
	t.Helper()
	token, ck := csrfFor(t, eng, cookie, "/admin")
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(session.CSRFHeader, token)
	r.AddCookie(ck)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	var out map[string]any
	if w.Body.Len() > 0 && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v; body: %s", err, w.Body.String())
		}
	}
	return w.Code, out
}

// uploadBody builds a multipart body with one part per file.
func uploadBody(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Sorted so the body is deterministic and a failing test is reproducible.
	for _, name := range slices.Sorted(maps.Keys(files)) {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), mw.FormDataContentType()
}

func postUpload(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path, body, ctype string) (int, map[string]any) {
	t.Helper()
	token, ck := csrfFor(t, eng, cookie, "/admin")
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", ctype)
	r.Header.Set(session.CSRFHeader, token)
	r.AddCookie(ck)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	var out map[string]any
	if w.Body.Len() > 0 && strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v; body: %s", err, w.Body.String())
		}
	}
	return w.Code, out
}

func TestFileManagerUpload_SavesTheGoodOnesAndReportsTheBadOne(t *testing.T) {
	// This is the assertion a single http.MaxBytesReader implementation fails:
	// one oversized file among several must be an item error, not a dead body.
	eng, adm, cookie := fmStack(t)
	adm.Storage.SetMaxUploadSizeForTest(4)

	body, ctype := uploadBody(t, map[string]string{
		"ok.png":  "abc",
		"big.png": "way too many bytes",
		"bad.exe": "x",
	})
	code, out := postUpload(t, eng, cookie, "/admin/filemanager/api/upload", body, ctype)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %+v", code, out)
	}
	entries, _ := out["entries"].([]any)
	if len(entries) != 1 {
		t.Errorf("entries = %v, want only ok.png", out["entries"])
	}
	fails, _ := out["errors"].([]any)
	if len(fails) != 2 {
		t.Errorf("errors = %v, want big.png and bad.exe", out["errors"])
	}
	if _, err := adm.Storage.Stat(context.Background(), "ok.png"); err != nil {
		t.Errorf("the good file must be on disk: %v", err)
	}
}

func TestFileManagerUpload_OverTheRequestCeilingIsAWholeRequestFailure(t *testing.T) {
	eng, adm, cookie := fmStack(t)
	adm.Storage.SetMaxRequestSizeForTest(32)

	body, ctype := uploadBody(t, map[string]string{"a.png": strings.Repeat("x", 512)})
	code, out := postUpload(t, eng, cookie, "/admin/filemanager/api/upload", body, ctype)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", code)
	}
	if _, ok := out["errors"]; ok {
		t.Error("a body-level failure must not pretend to be a per-item report")
	}
}

func TestFileManagerMkdirRenameMoveDelete(t *testing.T) {
	eng, adm, cookie := fmStack(t)
	ctx := context.Background()

	if code, out := postJSON(t, eng, cookie, "/admin/filemanager/api/mkdir",
		`{"path":"","name":"photos"}`); code != http.StatusOK {
		t.Fatalf("mkdir: status = %d, body %+v", code, out)
	}
	if code, _ := postJSON(t, eng, cookie, "/admin/filemanager/api/mkdir",
		`{"path":"","name":"photos"}`); code != http.StatusConflict {
		t.Errorf("a second mkdir: status = %d, want 409", code)
	}

	if _, err := adm.Storage.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if code, out := postJSON(t, eng, cookie, "/admin/filemanager/api/rename",
		`{"path":"a.png","name":"b.png"}`); code != http.StatusOK {
		t.Fatalf("rename: status = %d, body %+v", code, out)
	}
	if _, err := adm.Storage.Stat(ctx, "b.png"); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}

	if code, out := postJSON(t, eng, cookie, "/admin/filemanager/api/move",
		`{"paths":["b.png","ghost.png"],"to":"photos"}`); code != http.StatusOK {
		t.Fatalf("move: status = %d, body %+v", code, out)
	} else if fails, _ := out["errors"].([]any); len(fails) != 1 {
		t.Errorf("move errors = %v, want just ghost.png", out["errors"])
	}
	if _, err := adm.Storage.Stat(ctx, "photos/b.png"); err != nil {
		t.Errorf("moved file missing: %v", err)
	}

	if code, out := postJSON(t, eng, cookie, "/admin/filemanager/api/delete",
		`{"paths":["photos"]}`); code != http.StatusOK {
		t.Fatalf("delete: status = %d, body %+v", code, out)
	}
	if _, err := adm.Storage.Stat(ctx, "photos"); err == nil {
		t.Error("delete must be recursive")
	}
}

func TestFileManagerMutations_RequireTheCSRFHeader(t *testing.T) {
	eng, _, cookie := fmStack(t)
	r := httptest.NewRequest(http.MethodPost, "/admin/filemanager/api/mkdir",
		strings.NewReader(`{"path":"","name":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 without a token", w.Code)
	}
}
```

Add the new imports to the test file: `bytes`, `maps`, `mime/multipart`,
`slices`, and `github.com/millken/goapp-template/internal/service/session`.

- [ ] **Step 2: Add the two test seams to the storage service**

The size caps come from config, and a test needs to lower them without writing
a config file. Add to `internal/service/storage/storage.go`:

```go
// SetMaxUploadSizeForTest and SetMaxRequestSizeForTest lower the caps from a
// test. They exist because the two limits have different failure shapes (§5.2)
// and proving that needs both to be reachable; production sets them in config.
func (s *Service) SetMaxUploadSizeForTest(n int64)  { s.cfg.MaxUploadSize = n }
func (s *Service) SetMaxRequestSizeForTest(n int64) { s.cfg.MaxRequestSize = n }
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/controller/admin/ -run TestFileManager -count=1`
Expected: FAIL — the mutation tests get 501 from the stubs.

- [ ] **Step 4: Replace the stubs**

In `internal/controller/admin/filemanager.go`, delete the five stubs and add:

```go
// fmUpload streams a multipart body straight to storage.
//
// Two failure classes, deliberately kept apart (§5.2). A per-file rejection —
// extension, size, an illegal name — is an item error: the part is drained, the
// remaining parts are still processed, and the response is 200 with the failure
// listed. A body-level failure — the request ceiling, a malformed body, a
// dropped connection — kills the request, because there are no further parts to
// report on. Collapsing the two into one http.MaxBytesReader would turn "one of
// your five files is too big" into "your upload failed", which is a worse
// answer and a harder one to act on.
//
// The target directory rides in the query string rather than a form field: parts
// arrive in order, and a field placed after the files would be read too late.
func (a *Admin) fmUpload(c *inertia.Context) {
	ctx := c.Request.Context()
	dir := c.Query("path")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, a.Storage.MaxRequestSize())
	mr, err := c.Request.MultipartReader()
	if err != nil {
		a.fmBodyFail(c, err)
		return
	}

	entries := []fmEntry{}
	fails := []map[string]string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			a.fmBodyFail(c, err)
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			_ = part.Close()
			continue
		}

		name := part.FileName()
		e, err := a.Storage.Upload(ctx, dir, name, part)
		if err != nil {
			// Every error from Upload is this item's alone — §5.2's table puts
			// a backend failure in the item class alongside the policy ones,
			// and Move/Delete already work this way. The caller learns more
			// from a per-file report than from a batch truncated at the first
			// error, so the loop always continues to the next part.
			fails = append(fails, map[string]string{"name": name, "error": itemReason(err)})
			// part.Close drains whatever was not read, so nothing else is
			// needed before moving on.
			_ = part.Close()
			continue
		}
		_ = part.Close()

		full := e.Name
		if d := cleanQueryDir(dir); d != "" {
			full = d + "/" + e.Name
		}
		entries = append(entries, fmEntry{
			Name: e.Name, Path: full, Dir: false, Size: e.Size,
			MTime: e.ModTime.Unix(), URL: a.Storage.URLFor(full),
		})
	}

	a.fmOK(c, map[string]any{"entries": entries, "errors": fails})
}

// cleanQueryDir normalises the upload target for building response paths. The
// service already validated it — anything illegal failed before we got here.
func cleanQueryDir(dir string) string { return strings.Trim(dir, "/") }

// fmBodyFail answers a body-level upload failure. Deliberately no "errors" key:
// a per-item report would claim knowledge of parts that were never read.
func (a *Admin) fmBodyFail(c *inertia.Context, err error) {
	var tooLarge *http.MaxBytesError
	status, msg := http.StatusBadRequest, "上传内容无法解析"
	if errors.As(err, &tooLarge) {
		status, msg = http.StatusRequestEntityTooLarge, "上传内容超过单次请求上限"
	}
	c.Status(status)
	if err := c.JSON(map[string]string{"error": msg}); err != nil {
		slog.Error("filemanager: write error json", "err", err)
	}
}

// itemReason is the per-item message for a policy rejection.
func itemReason(err error) string {
	if errors.Is(err, storage.ErrRejected) || errors.Is(err, storage.ErrBadPath) {
		return err.Error()
	}
	return "保存失败"
}

// fmMkdir creates one directory.
func (a *Admin) fmMkdir(c *inertia.Context) {
	var req struct{ Path, Name string }
	if !a.fmDecode(c, &req) {
		return
	}
	if err := a.Storage.Mkdir(c.Request.Context(), req.Path, req.Name); err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{})
}

// fmRename renames one entry inside its own directory.
func (a *Admin) fmRename(c *inertia.Context) {
	var req struct{ Path, Name string }
	if !a.fmDecode(c, &req) {
		return
	}
	if err := a.Storage.Rename(c.Request.Context(), req.Path, req.Name); err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{})
}

// fmMove relocates entries into another directory.
func (a *Admin) fmMove(c *inertia.Context) {
	var req struct {
		Paths []string `json:"paths"`
		To    string   `json:"to"`
	}
	if !a.fmDecode(c, &req) {
		return
	}
	fails, err := a.Storage.Move(c.Request.Context(), req.Paths, req.To)
	if err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{"errors": itemErrors(fails)})
}

// fmDelete removes entries, recursively for directories.
func (a *Admin) fmDelete(c *inertia.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !a.fmDecode(c, &req) {
		return
	}
	fails, err := a.Storage.Delete(c.Request.Context(), req.Paths)
	if err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{"errors": itemErrors(fails)})
}

// fmDecode reads a JSON request body, answering 422 and reporting false when it
// cannot. The 1MB ceiling is for a body of paths; anything larger is not one.
func (a *Admin) fmDecode(c *inertia.Context, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		c.Status(http.StatusUnprocessableEntity)
		if err := c.JSON(map[string]string{"error": "请求格式不正确"}); err != nil {
			slog.Error("filemanager: write error json", "err", err)
		}
		return false
	}
	return true
}

// itemErrors renders per-item failures in the response shape §5.2 fixes.
func itemErrors(in []storage.ItemError) []map[string]string {
	out := make([]map[string]string, 0, len(in))
	for _, e := range in {
		out = append(out, map[string]string{"name": e.Name, "error": e.Reason})
	}
	return out
}
```

Add `encoding/json`, `io` and `strings` to the file's imports.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/controller/admin/ -run TestFileManager -count=1 -v`
Expected: PASS, ten tests.

If `TestFileManagerUpload_SavesTheGoodOnesAndReportsTheBadOne` reports zero
entries and one error, the implementation is using `ParseMultipartForm` or a
whole-body `MaxBytesReader` somewhere — that is exactly the collapse this test
exists to catch.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/admin internal/service/storage
git commit -m "feat(admin): file manager mutations, with per-item and per-request failures kept apart"
```

---

### Task 6: The FileManager component and its page

**Files:**
- Create: `frontend/src/components/admin/FileManager.vue`, `frontend/src/components/admin/FileManager.test.ts`, `frontend/pages/admin/filemanager/index.vue`
- Modify: `server/ssr_admin_test.go`

**Interfaces:**
- Consumes: the JSON API from Tasks 4–5.
- Produces:
  ```ts
  // FileManager.vue
  defineProps<{
    basePath: string          // "/admin/filemanager"
    urlPrefix: string         // "/uploads"
    csrfToken?: string
    mode?: 'manage' | 'pick' | 'dirs'   // default 'manage'
  }>()
  defineEmits<{ (e: 'select', entry: FmEntry): void }>()
  export type FmEntry = { name: string; path: string; dir: boolean; size: number; mtime: number; url: string }
  ```

- [ ] **Step 1: Write the failing test**

Create `frontend/src/components/admin/FileManager.test.ts`:

```ts
// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import FileManager from './FileManager.vue'

// Mounted via createApp directly: the repo deliberately has no @vue/test-utils.
function mount(props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp({
    render: () =>
      h(FileManager, { basePath: '/admin/filemanager', urlPrefix: '/uploads', ...props } as any),
  })
  app.mount(el)
  return el
}

const listing = (over: Record<string, unknown> = {}) => ({
  ok: true,
  path: '',
  breadcrumb: [{ name: '全部文件', path: '' }],
  entries: [
    { name: 'photos', path: 'photos', dir: true, size: 0, mtime: 0, url: '/uploads/photos' },
    { name: 'a.png', path: 'a.png', dir: false, size: 3, mtime: 0, url: '/uploads/a.png' },
  ],
  total: 2,
  page: 1,
  pageSize: 40,
  ...over,
})

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn(async () => new Response(JSON.stringify(listing()), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('FileManager', () => {
  it('lists the directory it was given on mount', async () => {
    const el = mount()
    await nextTick()
    await nextTick()
    expect(fetchMock).toHaveBeenCalled()
    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain('/admin/filemanager/api/list')
    expect(el.textContent).toContain('a.png')
    expect(el.textContent).toContain('photos')
  })

  it('sends the CSRF token on a mutation', async () => {
    const el = mount({ csrfToken: 'tok' })
    await nextTick()
    await nextTick()
    const button = [...el.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('新建目录'),
    )
    expect(button).toBeTruthy()
    // The dialog is not driven here — call the exposed helper the button uses.
    // A mkdir needs a name, so drive it through the component's own API.
    const input = el.querySelector('input[data-testid="mkdir-name"]') as HTMLInputElement | null
    if (input) {
      input.value = 'newdir'
      input.dispatchEvent(new Event('input'))
    }
    button!.click()
    await nextTick()
    const call = fetchMock.mock.calls.find(([u]) => String(u).includes('/api/mkdir'))
    expect(call).toBeTruthy()
    const init = call![1] as RequestInit
    expect((init.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok')
  })

  it('emits select in pick mode instead of navigating', async () => {
    const selected: unknown[] = []
    const el = document.createElement('div')
    document.body.appendChild(el)
    createApp({
      render: () =>
        h(FileManager, {
          basePath: '/admin/filemanager',
          urlPrefix: '/uploads',
          mode: 'pick',
          onSelect: (e: unknown) => selected.push(e),
        } as any),
    }).mount(el)
    await nextTick()
    await nextTick()

    const file = [...el.querySelectorAll('[data-entry]')].find(
      (n) => n.getAttribute('data-entry') === 'a.png',
    ) as HTMLElement
    expect(file).toBeTruthy()
    file.click()
    await nextTick()
    expect(selected).toHaveLength(1)
    expect((selected[0] as { path: string }).path).toBe('a.png')
  })

  it('hides files entirely in dirs mode', async () => {
    const el = mount({ mode: 'dirs' })
    await nextTick()
    await nextTick()
    expect(el.textContent).toContain('photos')
    expect(el.textContent).not.toContain('a.png')
  })

  it('pages through a total larger than one page', async () => {
    fetchMock.mockImplementation(
      async () =>
        new Response(JSON.stringify(listing({ total: 100, page: 1, pageSize: 40 })), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const el = mount()
    await nextTick()
    await nextTick()
    expect(el.textContent).toContain('100')
    const next = [...el.querySelectorAll('button')].find(
      (b) => b.getAttribute('data-testid') === 'next-page',
    )
    expect(next).toBeTruthy()
    next!.click()
    await nextTick()
    const last = String(fetchMock.mock.calls.at(-1)![0])
    expect(last).toContain('page=2')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm -C frontend run test -- FileManager`
Expected: FAIL — cannot resolve `./FileManager.vue`.

- [ ] **Step 3: Write the component**

Create `frontend/src/components/admin/FileManager.vue`:

```vue
<script setup lang="ts">
// The media library, in three shapes: the standalone page (mode="manage"), the
// picker inside a dialog (mode="pick"), and the move-target chooser
// (mode="dirs"). One component rather than three because the listing, the
// breadcrumb and the pager are identical in all of them — only what a click
// means differs.
//
// Directory changes are component state, never navigation: routing them
// through Inertia would put every `cd` in the browser history, and the back
// button inside a modal would then mean "go up one folder" instead of "close".
import { computed, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, File, Folder, Upload } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export type FmEntry = {
  name: string
  path: string
  dir: boolean
  size: number
  mtime: number
  url: string
}
type Crumb = { name: string; path: string }

const props = withDefaults(
  defineProps<{
    basePath: string
    urlPrefix: string
    csrfToken?: string
    mode?: 'manage' | 'pick' | 'dirs'
  }>(),
  { mode: 'manage' },
)

const emit = defineEmits<{ (e: 'select', entry: FmEntry): void }>()

const path = ref('')
const query = ref('')
const page = ref(1)
const entries = ref<FmEntry[]>([])
const breadcrumb = ref<Crumb[]>([])
const total = ref(0)
const pageSize = ref(40)
const busy = ref(false)
const error = ref('')
const selected = ref<Set<string>>(new Set())
const newDirName = ref('')
const uploadInput = ref<HTMLInputElement | null>(null)

const visible = computed(() =>
  props.mode === 'dirs' ? entries.value.filter((e) => e.dir) : entries.value,
)
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const canMutate = computed(() => props.mode === 'manage')

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({
      path: path.value,
      q: query.value,
      page: String(page.value),
    })
    const res = await fetch(`${props.basePath}/api/list?${params}`, {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
    })
    const body = await res.json()
    if (!res.ok) {
      error.value = body?.error ?? '读取失败'
      return
    }
    entries.value = body.entries ?? []
    breadcrumb.value = body.breadcrumb ?? []
    total.value = body.total ?? 0
    pageSize.value = body.pageSize ?? 40
    selected.value = new Set()
  } catch {
    error.value = '网络错误'
  } finally {
    busy.value = false
  }
}

// The token is what makes a mutation legal (see csrf.go); a missing one is a
// 403 the user cannot act on, so the buttons are disabled without it.
async function mutate(action: string, body: unknown): Promise<Record<string, unknown> | null> {
  busy.value = true
  error.value = ''
  try {
    const res = await fetch(`${props.basePath}/api/${action}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': props.csrfToken ?? '',
      },
      body: JSON.stringify(body),
    })
    const out = await res.json().catch(() => null)
    if (!res.ok) {
      error.value = (out as { error?: string })?.error ?? '操作失败'
      return null
    }
    const fails = (out as { errors?: { name: string; error: string }[] })?.errors ?? []
    if (fails.length) {
      error.value = fails.map((f) => `${f.name}：${f.error}`).join('；')
    }
    await refresh()
    return out as Record<string, unknown>
  } catch {
    error.value = '网络错误'
    return null
  } finally {
    busy.value = false
  }
}

function enter(entry: FmEntry) {
  if (entry.dir) {
    path.value = entry.path
    page.value = 1
    return
  }
  if (props.mode === 'pick') emit('select', entry)
}

function go(to: string) {
  path.value = to
  page.value = 1
}

function toggle(entry: FmEntry) {
  const next = new Set(selected.value)
  if (next.has(entry.path)) next.delete(entry.path)
  else next.add(entry.path)
  selected.value = next
}

async function mkdir() {
  const name = newDirName.value.trim()
  if (!name) return
  await mutate('mkdir', { path: path.value, name })
  newDirName.value = ''
}

async function removeSelected() {
  if (!selected.value.size) return
  await mutate('delete', { paths: [...selected.value] })
}

async function rename(entry: FmEntry) {
  const name = window.prompt('新名称', entry.name)?.trim()
  if (!name || name === entry.name) return
  await mutate('rename', { path: entry.path, name })
}

async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = input.files
  if (!files?.length) return
  const form = new FormData()
  for (const f of files) form.append('files', f)

  busy.value = true
  error.value = ''
  try {
    const res = await fetch(
      `${props.basePath}/api/upload?path=${encodeURIComponent(path.value)}`,
      {
        method: 'POST',
        credentials: 'same-origin',
        // Content-Type is deliberately unset: the browser adds the multipart
        // boundary, and setting it by hand produces a body the server cannot
        // parse.
        headers: { 'X-CSRF-Token': props.csrfToken ?? '' },
        body: form,
      },
    )
    const out = await res.json().catch(() => null)
    if (!res.ok) {
      error.value = (out as { error?: string })?.error ?? '上传失败'
    } else {
      const fails = (out as { errors?: { name: string; error: string }[] })?.errors ?? []
      if (fails.length) error.value = fails.map((f) => `${f.name}：${f.error}`).join('；')
    }
    await refresh()
  } catch {
    error.value = '网络错误'
  } finally {
    busy.value = false
    input.value = ''
  }
}

watch([path, page], refresh, { immediate: true })
watch(query, () => {
  page.value = 1
  refresh()
})
</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center gap-2">
      <nav class="flex items-center gap-1 text-sm text-muted-foreground">
        <template v-for="(crumb, i) in breadcrumb" :key="crumb.path">
          <span v-if="i > 0">/</span>
          <button type="button" class="hover:text-foreground" @click="go(crumb.path)">
            {{ crumb.name }}
          </button>
        </template>
      </nav>

      <div class="ml-auto flex items-center gap-2">
        <Input
          v-model="query"
          placeholder="搜索当前目录"
          class="h-9 w-48"
          data-testid="search"
        />
        <template v-if="canMutate">
          <Input
            v-model="newDirName"
            placeholder="新目录名"
            class="h-9 w-32"
            data-testid="mkdir-name"
          />
          <Button type="button" variant="outline" :disabled="busy" @click="mkdir">
            新建目录
          </Button>
          <Button type="button" variant="outline" :disabled="busy" @click="uploadInput?.click()">
            <Upload class="mr-1 size-4" /> 上传
          </Button>
          <input
            ref="uploadInput"
            type="file"
            multiple
            class="hidden"
            data-testid="upload-input"
            @change="upload"
          />
          <Button
            type="button"
            variant="destructive"
            :disabled="busy || !selected.size"
            @click="removeSelected"
          >
            删除
          </Button>
        </template>
      </div>
    </div>

    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

    <ul class="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
      <li
        v-for="entry in visible"
        :key="entry.path"
        :data-entry="entry.path"
        class="group cursor-pointer rounded-md border p-2 text-center hover:bg-accent"
        :class="selected.has(entry.path) ? 'ring-2 ring-primary' : ''"
        @click="enter(entry)"
      >
        <div class="flex h-20 items-center justify-center overflow-hidden">
          <Folder v-if="entry.dir" class="size-10 text-muted-foreground" />
          <img
            v-else-if="/\.(png|jpe?g|gif|webp)$/i.test(entry.name)"
            :src="entry.url"
            :alt="entry.name"
            loading="lazy"
            class="max-h-20 max-w-full object-contain"
          />
          <File v-else class="size-10 text-muted-foreground" />
        </div>
        <p class="mt-1 truncate text-xs" :title="entry.name">{{ entry.name }}</p>
        <div v-if="canMutate" class="mt-1 flex justify-center gap-2 text-xs">
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground"
            @click.stop="toggle(entry)"
          >
            {{ selected.has(entry.path) ? '取消选择' : '选择' }}
          </button>
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground"
            @click.stop="rename(entry)"
          >
            重命名
          </button>
        </div>
      </li>
    </ul>

    <p v-if="!visible.length && !busy" class="py-8 text-center text-sm text-muted-foreground">
      这个目录是空的
    </p>

    <div class="flex items-center justify-end gap-2 text-sm text-muted-foreground">
      <span>共 {{ total }} 项</span>
      <Button
        type="button"
        variant="outline"
        size="icon"
        class="size-9"
        data-testid="prev-page"
        :disabled="page <= 1"
        @click="page = page - 1"
      >
        <ChevronLeft class="size-4" />
      </Button>
      <Button
        type="button"
        variant="outline"
        size="icon"
        class="size-9"
        data-testid="next-page"
        :disabled="page >= pages"
        @click="page = page + 1"
      >
        <ChevronRight class="size-4" />
      </Button>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Run the component tests**

Run: `pnpm -C frontend run test -- FileManager`
Expected: PASS, five tests.

- [ ] **Step 5: Write the page**

Create `frontend/pages/admin/filemanager/index.vue`:

```vue
<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FileManager from '@/components/admin/FileManager.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Card, CardContent } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number; section?: string }

defineProps<{
  basePath: string
  urlPrefix: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  csrfToken?: string
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :csrf-token="csrfToken"
  >
    <PageHeader title="文件" />
    <Card>
      <CardContent class="pt-6">
        <FileManager :base-path="basePath" :url-prefix="urlPrefix" :csrf-token="csrfToken" />
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

- [ ] **Step 6: Add the SSR render test**

In `server/ssr_admin_test.go`, add a test alongside the dashboard one:

```go
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
	for _, want := range []string{"新建目录", "上传", "全部文件"} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
}
```

- [ ] **Step 7: Build and verify everything**

```bash
pnpm -C frontend run test
pnpm -C frontend run type-check
pnpm -C frontend run build
go test ./server/ -run TestSSR_FileManager -count=1 -v
```
Expected: all pass. The SSR test skips if no bundle exists — the `build` above
produces one, so a skip here means the build did not run.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/admin/FileManager.vue frontend/src/components/admin/FileManager.test.ts frontend/pages/admin/filemanager server/ssr_admin_test.go
git commit -m "feat(admin-ui): the file manager, one component for page picker and mover"
```

---

### Task 7: The dialog, the image picker, and the degraded fallback

**Files:**
- Create: `frontend/src/components/admin/FileManagerDialog.vue`, `frontend/src/components/admin/ImagePicker.vue`, `frontend/src/components/admin/ImagePicker.test.ts`
- Modify: `internal/controller/admin/auth.go`

**Interfaces:**
- Consumes: `FileManager.vue`, `FmEntry` (Task 6).
- Produces:
  ```ts
  // ImagePicker.vue — a control for FormField's slot
  defineProps<{
    name: string; modelValue?: string; basePath?: string; urlPrefix?: string
    csrfToken?: string; canBrowse?: boolean
  }>()
  defineEmits<{ (e: 'update:modelValue', v: string): void }>()
  ```
  Go side: `canBrowseFiles` prop, set by `resolve`.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/components/admin/ImagePicker.test.ts`:

```ts
// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import ImagePicker from './ImagePicker.vue'

function mount(props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(ImagePicker, { name: 'avatar', ...props } as any) }).mount(el)
  return el
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ImagePicker', () => {
  it('always submits its value through a named input', () => {
    const el = mount({ modelValue: 'a/b.png' })
    const input = el.querySelector('input[name="avatar"]') as HTMLInputElement
    expect(input).toBeTruthy()
    expect(input.value).toBe('a/b.png')
  })

  it('offers the picker when the caller may browse', () => {
    const el = mount({ canBrowse: true })
    expect(el.textContent).toContain('选择图片')
    expect(el.querySelector('input[type="text"][name="avatar"]')).toBeNull()
  })

  it('degrades to a text field when the caller may not browse', async () => {
    // A user with user.modify but no filemanager.access would get a 403 from
    // the picker's endpoints, so the button must not be offered at all.
    const el = mount({ canBrowse: false, modelValue: 'a/b.png' })
    await nextTick()
    expect(el.textContent).not.toContain('选择图片')
    const text = el.querySelector('input[type="text"][name="avatar"]') as HTMLInputElement
    expect(text).toBeTruthy()
    expect(text.value).toBe('a/b.png')
  })

  it('clears the value', async () => {
    const el = mount({ canBrowse: true, modelValue: 'a/b.png' })
    const clear = [...el.querySelectorAll('button')].find((b) => b.textContent?.includes('清除'))
    expect(clear).toBeTruthy()
    clear!.click()
    await nextTick()
    const input = el.querySelector('input[name="avatar"]') as HTMLInputElement
    expect(input.value).toBe('')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `pnpm -C frontend run test -- ImagePicker`
Expected: FAIL — cannot resolve `./ImagePicker.vue`.

- [ ] **Step 3: Write the dialog**

Create `frontend/src/components/admin/FileManagerDialog.vue`:

```vue
<script setup lang="ts">
// The picker shell: a Dialog around FileManager in pick (or dirs) mode. It owns
// nothing but open state — the listing, the mutations and the errors all stay
// in FileManager, so the page and the modal cannot drift apart.
import FileManager, { type FmEntry } from '@/components/admin/FileManager.vue'
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

withDefaults(
  defineProps<{
    open: boolean
    basePath?: string
    urlPrefix?: string
    csrfToken?: string
    mode?: 'pick' | 'dirs'
    title?: string
  }>(),
  {
    basePath: '/admin/filemanager',
    urlPrefix: '/uploads',
    mode: 'pick',
    title: '选择文件',
  },
)

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'select', entry: FmEntry): void
}>()

function choose(entry: FmEntry) {
  emit('select', entry)
  emit('update:open', false)
}
</script>

<template>
  <Dialog :open="open" @update:open="(v: boolean) => emit('update:open', v)">
    <DialogContent class="max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>点击文件夹进入，点击文件选中。</DialogDescription>
      </DialogHeader>
      <FileManager
        :base-path="basePath"
        :url-prefix="urlPrefix"
        :csrf-token="csrfToken"
        :mode="mode"
        @select="choose"
      />
    </DialogContent>
  </Dialog>
</template>
```

`FileManager.vue` must export its entry type for this import to type-check. Add
to its `<script setup>` (it is already declared there as `export type FmEntry`)
— confirm `pnpm -C frontend run type-check` accepts the import; if it does not,
move the type into `frontend/src/components/admin/filemanager-types.ts` and
import it from both files.

- [ ] **Step 4: Write the picker**

Create `frontend/src/components/admin/ImagePicker.vue`:

```vue
<script setup lang="ts">
// A control for FormField's slot: a preview, a button that opens the library,
// and a hidden input carrying the chosen path so a plain form POST submits it.
//
// canBrowse is the caller's filemanager.access, delivered as a page prop. When
// it is false this degrades to a text input: the picker's endpoints would 403,
// and a button guaranteed to fail is worse than a field the user can still type
// into.
import { computed, ref } from 'vue'
import FileManagerDialog from '@/components/admin/FileManagerDialog.vue'
import type { FmEntry } from '@/components/admin/FileManager.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = withDefaults(
  defineProps<{
    name: string
    modelValue?: string
    basePath?: string
    urlPrefix?: string
    csrfToken?: string
    canBrowse?: boolean
  }>(),
  { modelValue: '', basePath: '/admin/filemanager', urlPrefix: '/uploads', canBrowse: false },
)

const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

const open = ref(false)
const value = ref(props.modelValue)
const preview = computed(() => (value.value ? `${props.urlPrefix}/${value.value}` : ''))

function set(v: string) {
  value.value = v
  emit('update:modelValue', v)
}
</script>

<template>
  <div class="space-y-2">
    <div v-if="preview" class="flex items-center gap-3">
      <img :src="preview" :alt="value" class="size-16 rounded border object-cover" />
      <span class="truncate text-xs text-muted-foreground">{{ value }}</span>
    </div>

    <template v-if="canBrowse">
      <input type="hidden" :name="name" :value="value" />
      <div class="flex gap-2">
        <Button type="button" variant="outline" @click="open = true">选择图片</Button>
        <Button v-if="value" type="button" variant="ghost" @click="set('')">清除</Button>
      </div>
      <FileManagerDialog
        v-model:open="open"
        :base-path="basePath"
        :url-prefix="urlPrefix"
        :csrf-token="csrfToken"
        title="选择图片"
        @select="(entry: FmEntry) => set(entry.path)"
      />
    </template>

    <template v-else>
      <Input
        type="text"
        :name="name"
        :model-value="value"
        placeholder="图片路径，例如 photos/a.png"
        @update:model-value="(v: string | number) => set(String(v))"
      />
    </template>
  </div>
</template>
```

- [ ] **Step 5: Deliver `canBrowseFiles`**

In `internal/controller/admin/auth.go`, in `resolve`, next to the other
`c.Set` calls:

```go
	//goappctl:storage
	// One prop, read by one component (ImagePicker), set here rather than in
	// the handlers that render it. resolve is the only place already holding
	// the caller's group: a page handler would have to look it up again, and
	// the admin area's rule is one query per request. Deriving it client-side
	// from adminMenu was the alternative and was rejected — it would make the
	// picker's behaviour depend on a sidebar entry existing.
	c.Set("canBrowseFiles", cl.group.Superuser ||
		cl.group.Permissions.Allows("filemanager"+verbAccess))
	//goappctl:end
```

Place it immediately before the `csrfToken` block so the storage marker does not
straddle unrelated lines.

- [ ] **Step 6: Add the Go-side test**

Append to `internal/controller/admin/filemanager_test.go`:

```go
func TestResolve_DeliversCanBrowseFiles(t *testing.T) {
	// The superuser seeded by loginStack may browse; the prop has to say so, or
	// every image field in the admin degrades to a text box.
	eng, _, cookie := fmStack(t)
	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "canBrowseFiles") {
		t.Fatalf("the dashboard page carries no canBrowseFiles prop; body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `canBrowseFiles\":true`) &&
		!strings.Contains(w.Body.String(), `canBrowseFiles":true`) {
		t.Errorf("canBrowseFiles is not true for a superuser; body: %s", w.Body.String())
	}
}
```

- [ ] **Step 7: Run everything**

```bash
pnpm -C frontend run test
pnpm -C frontend run type-check
go test ./internal/controller/admin/ -count=1
```
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/admin internal/controller/admin
git commit -m "feat(admin-ui): an image picker that degrades instead of 403ing"
```

---

### Task 8: The consumer — an avatar on the admin user

**Files:**
- Create: `internal/service/db/migrations/006_user_avatar.up.sql`, `internal/service/db/migrations/006_user_avatar.down.sql`
- Modify: `internal/controller/admin/user_crud.go`, `frontend/pages/admin/user/form.vue`, `frontend/pages/admin/user/index.vue`, `server/ssr_admin_test.go`

**Interfaces:**
- Consumes: `ImagePicker.vue` (Task 7), `Storage.ValidatePath` (Task 2).
- Produces: `userRow.Avatar string \`json:"avatar"\``.

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/admin/user_crud_test.go`:

```go
func TestUserAvatar_RoundTripsAndIsValidated(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	stor := storage.New(&storage.Config{Root: t.TempDir()})
	if err := stor.Start(ctx); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(ctx) })
	adm.Storage = stor

	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, "/admin/user", url.Values{
		"username": {"dave"},
		"password": {"s3cretpw"},
		"group_id": {fmt.Sprint(gid)},
		"avatar":   {"photos/dave.png"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	var avatar string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT avatar FROM users WHERE username = 'dave'`).Scan(&avatar); err != nil {
		t.Fatal(err)
	}
	if avatar != "photos/dave.png" {
		t.Errorf("avatar = %q", avatar)
	}

	// A path that escapes the tree is a form error, not a stored string.
	w = post(t, eng, cookie, "/admin/user", url.Values{
		"username": {"erin"},
		"password": {"s3cretpw"},
		"group_id": {fmt.Sprint(gid)},
		"avatar":   {"../../etc/passwd"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("an invalid avatar must re-render the form, got %d", w.Code)
	}
	var n int
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE username = 'erin'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("erin must not have been created")
	}
}
```

Add `"github.com/millken/goapp-template/internal/service/storage"` to the test
file's imports.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/controller/admin/ -run TestUserAvatar -count=1`
Expected: FAIL — `no such column: avatar`.

- [ ] **Step 3: Write the migration**

Create `internal/service/db/migrations/006_user_avatar.up.sql`:

```sql
-- 006_user_avatar.up.sql
-- The user's avatar as a path relative to the storage root, or '' for none.
-- A path, not a URL: the public prefix is config (storage.url_prefix), and
-- baking it into stored data would make changing it a data migration.
--
-- Not wrapped in a goappctl marker, unlike the Go and Vue code that uses it: a
-- column nobody writes to is harmless, while a conditionally-numbered migration
-- would make two generated projects disagree about what 006 is.
--
-- No backfill: a constant DEFAULT on ADD COLUMN fills existing rows.
--
-- SQLite flavor (the template's default driver). PostgreSQL and MySQL accept
-- this statement as written.
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
```

Create `internal/service/db/migrations/006_user_avatar.down.sql`:

```sql
-- 006_user_avatar.down.sql
-- Discards every stored avatar path. The files themselves stay in the storage
-- tree — this migration never owned them.
--
-- DROP COLUMN needs SQLite 3.35+ (mattn/go-sqlite3 bundles 3.53); MySQL and
-- PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN avatar;
```

- [ ] **Step 4: Thread it through the handler**

In `internal/controller/admin/user_crud.go`. **The column is unconditional; only
the code that touches `a.Storage` is marked.** Migration 006 always runs, so
`users.avatar` always exists — which means the struct field, the SELECT, the
Scan, the INSERT and the UPDATE need no markers at all. This is not a shortcut:
those statements are raw string literals, and a marker line inside one would be
stripped as SQL rather than as a comment.

Add the field to `userRow`, unmarked:

```go
	Avatar   string `json:"avatar"`
```

In `usersIndex`, add `u.avatar` to the SELECT list and `&u.Avatar` to the `Scan`
in the same position — unmarked, both.

In `userCreate`, add `avatar` to the INSERT column list, `?` to its VALUES, and
`item.Avatar` to the `ExecContext` arguments in the same position. Do the same
for the UPDATE in `userUpdate`. Unmarked.

Mark only the two places that need the service. In `userCreate` and
`userUpdate`, reading the field:

```go
	//goappctl:storage
	// Marked, so a storage-less build never stores a path it has no way to
	// validate or render: with the component off this line is gone and Avatar
	// stays "".
	item.Avatar = c.PostForm("avatar")
	//goappctl:end
```

and in `validateUser`, after the existing rules — `Validator` has no `AddError`;
its computed-condition method is `Check(ok bool, field, msg string)`, the same
one `userUpdate` uses for the self-group rule:

```go
	//goappctl:storage
	// A path from a form is a string a browser sent, and the picker is not the
	// only way to fill this field. Empty means "no avatar" and is valid.
	if item.Avatar != "" {
		v.Check(a.Storage.ValidatePath(item.Avatar) == nil, "avatar", "图片路径不合法")
	}
	//goappctl:end
```

- [ ] **Step 5: Add the picker to the form**

In `frontend/pages/admin/user/form.vue`, add to the props type:

```ts
  canBrowseFiles?: boolean
  urlPrefix?: string
```

extend `UserRow` with `avatar: string`, import the picker, and add the field
after the group select:

```vue
          <FormField name="avatar" label="头像" :error="errors?.avatar">
            <ImagePicker
              name="avatar"
              :model-value="item.avatar"
              :can-browse="canBrowseFiles"
              :url-prefix="urlPrefix ?? '/uploads'"
              :csrf-token="csrfToken"
            />
          </FormField>
```

In `frontend/pages/admin/user/index.vue`, extend `UserRow` with `avatar: string`
and render it in the username cell via the DataTable cell slot the other
columns use — check how `status` is rendered there and follow it exactly.

The Go handler must pass `urlPrefix`: add `c.Set("urlPrefix", a.Storage.URLPrefix())`
inside a `//goappctl:storage` block in `renderUserForm`.

- [ ] **Step 6: Extend the SSR test**

In `server/ssr_admin_test.go`, wherever the user form is rendered (or add a
render if it is not covered), pass `"canBrowseFiles": true` and an `item`
carrying `"avatar": "a.png"`, and assert the rendered HTML contains `选择图片`.
If no such test exists, add one modelled on
`TestSSR_FileManagerRendersUnderQuickJS` from Task 6, rendering
`admin/user/form`.

- [ ] **Step 7: Run everything**

```bash
go test ./... -count=1
pnpm -C frontend run test && pnpm -C frontend run type-check && pnpm -C frontend run build
go test ./server/ -run TestSSR -count=1
```
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/service/db/migrations internal/controller/admin frontend/pages/admin/user server/ssr_admin_test.go
git commit -m "feat(admin): an avatar on the user, so the image field has a consumer"
```

---

### Task 9: goappctl — make it strippable, and document it

The last task, and the one that decides whether `storage` is a component or just
code. A generated project that did not pick `storage` must compile with no trace
of it.

**Files:**
- Modify: `cmd/goappctl/internal/components/components.go`, `cmd/goappctl/internal/components/components_test.go`, `cmd/goappctl/internal/initcmd/initcmd_test.go`, `README.md`

**Interfaces:**
- Consumes: every marker block added in Tasks 3–8.
- Produces: `storage` as a selectable component name.

- [ ] **Step 1: Write the failing test**

In `cmd/goappctl/internal/components/components_test.go`, add cases to
`TestClosure`:

```go
		{"storage is independent", []string{"storage"}, []string{"storage"}, nil},
		{"storage with admin", []string{"admin", "storage"}, []string{"db", "session", "admin", "storage"}, []string{"db", "session"}},
```

and add a test pinning the ownership rule:

```go
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
```

Do **not** add an existence check for the owned paths: `TestOwnedPathsExist`
already stats every component's `Owned` entries, and it is why the list could
not be written in full before the files existed.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/goappctl/internal/components/ -count=1`
Expected: FAIL — `unknown component "storage"`.

- [ ] **Step 3: Complete the component's owned paths**

The entry itself already exists — it was added during Task 3, because
`markers.Strip` rejects a marker naming a component the registry does not know,
and Tasks 3–8 all add `goappctl:storage` blocks. It currently owns only the two
paths that existed then:

```go
	{
		Name: "storage",
		Owned: []string{
			"internal/service/storage",
			"server/uploads_route_test.go",
		},
	},
```

Extend `Owned` with the admin-side files, which now exist. They sit under
directories `admin` already owns; the overlap is harmless because deletion is
idempotent, and naming them individually is what makes "admin on, storage off"
strip correctly:

```go
			"internal/controller/admin/filemanager.go",
			"internal/controller/admin/filemanager_test.go",
			"frontend/pages/admin/filemanager",
			"frontend/src/components/admin/FileManager.vue",
			"frontend/src/components/admin/FileManager.test.ts",
			"frontend/src/components/admin/FileManagerDialog.vue",
			"frontend/src/components/admin/ImagePicker.vue",
			"frontend/src/components/admin/ImagePicker.test.ts",
```

Update the entry's comment to describe the finished list rather than the
partial one.

- [ ] **Step 4: Add the init combo**

In `cmd/goappctl/internal/initcmd/initcmd_test.go`, add to `TestRun_Combos`:

```go
		{
			// The combination the marker layout exists for: the admin area
			// present, its file manager gone. A leak here is a reference to a
			// deleted package, so this fails at build rather than subtly.
			name: "admin-without-storage", with: []string{"db", "session", "admin"},
			wantPresent: []string{"mattn/go-sqlite3"},
		},
		{
			// Storage with no admin: the service and the public route survive
			// with no UI at all.
			name: "storage-only", with: []string{"storage"},
			wantAbsent: []string{"mattn/go-sqlite3", "buke/quickjs-go"},
		},
```

Also add `"storage"` to the `all-on` combo's `with` list.

- [ ] **Step 5: Run the strip matrix**

```bash
go test ./cmd/goappctl/... -count=1
GOAPPCTL_E2E=1 go test ./cmd/goappctl/internal/initcmd/ -run TestRun_Combos -count=1 -v
```
Expected: PASS. The E2E run is slow (it builds and tests each generated project).
A failure in `admin-without-storage` naming `svc.Storage` or `ImagePicker` means
a marker block is missing from a file listed in Task 3–8's file lists — find it
by grepping the generated tree for `Storage` and `filemanager`.

- [ ] **Step 6: Update the README**

In `README.md`, add to the project-structure block, keeping the flat `├──` style
and wrapping each line in markers:

```
<!--goappctl:storage-->
├── internal/service/storage/    # 上传文件树（os.Root + Backend 接口）
<!--goappctl:end-->
<!--goappctl:storage-->
├── internal/controller/admin/filemanager.go  #   文件管理器：页面 + JSON API
<!--goappctl:end-->
<!--goappctl:storage-->
├── frontend/src/components/admin/FileManager.vue #   媒体库组件（页面/弹窗/移动目标共用）
<!--goappctl:end-->
```

Place them next to the related existing lines (service next to session, the
controller next to the admin controller line), not in a block at the end.

- [ ] **Step 7: Verify everything one last time**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
pnpm -C frontend run test && pnpm -C frontend run type-check && pnpm -C frontend run build
```
Expected: all pass, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add cmd/goappctl README.md
git commit -m "feat(goappctl): storage is a component, strippable in both directions"
```

---

## Self-review notes

Checked against the spec:

- §3 component boundary → Task 9; the marker blocks are added in the task that
  creates the code they wrap (3, 4, 7, 8).
- §4.1 config → Task 3. §4.2 contract → Task 1. §4.3 local → Task 1.
  §4.4 Service → Task 2.
- §5.1 public route and precedence → Task 3. §5.2 API, CSRF, response shapes and
  the two upload failure classes → Tasks 4–5. §5.3 `canBrowseFiles` → Task 7.
- §6 frontend → Tasks 6–7; the avatar consumer → Task 8.
- §7.1 conformance suite → Task 1, twelve subtests. §7.2 → distributed:
  path safety and upload policy in Task 2, failure classes in Task 5, route
  precedence in Task 3, handlers in Tasks 4–5, frontend in Tasks 6–7, SSR in
  Tasks 6 and 8, goappctl in Task 9.
- §8 trade-offs need no code. §9 out of scope is not implemented — in particular
  no `gen admin` image field type.

Three things the first draft got wrong and this one fixes, recorded because each
is a trap the implementer would otherwise hit:

1. **Markers inside SQL.** Task 8 originally wrapped the INSERT and UPDATE
   column lists in `//goappctl:storage`. Those are raw string literals — the
   marker would have been stripped as SQL, not as a comment. Resolved by making
   the column unconditional (migration 006 is unmarked anyway) and marking only
   the two statements that call `a.Storage`. Now a Global Constraint.
2. **`Validator` has no `AddError`.** Its computed-condition method is
   `Check(ok bool, field, msg string)`, already used in `userUpdate` for the
   self-group rule. Task 8 uses that.
3. **`local_test.go` must be `package storage_test`.** `backendtest` imports
   `storage`, so an internal test file importing it is an import cycle. Called
   out in Task 1 and in "Three things that shape the tasks".

Two places still tell the implementer to compare against the codebase rather
than trusting the plan, both because the target is a pattern rather than a
literal: the row-cell rendering style in `frontend/pages/admin/user/index.vue`
(Task 8, Step 5) and whether a user-form SSR render already exists (Task 8,
Step 6). Both are visible in the file being edited.
