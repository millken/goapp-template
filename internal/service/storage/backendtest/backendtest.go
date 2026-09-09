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
		defer func() { _ = f.Close() }()
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

	// Semantic 7: Rename refuses an existing destination. This is the case an
	// S3-style copy+delete backend gets backwards by default — a plain
	// "copy over, then delete the source" reproduces renameat(2)'s clobber
	// rather than refusing it — so without this case a second implementation
	// could pass the suite while overwriting exactly like the bug this
	// contract exists to prevent.
	t.Run("RenameRefusesAnExistingDestination", func(t *testing.T) {
		b := newBackend(t)
		mustSave(t, b, "keep.png", "original")
		mustSave(t, b, "other.png", "incoming")
		if err := b.Rename(ctx, "other.png", "keep.png"); !errors.Is(err, fs.ErrExist) {
			t.Errorf("Rename onto an existing name: err = %v, want fs.ErrExist", err)
		}
		if got := mustRead(t, b, "keep.png"); got != "original" {
			t.Errorf("destination content = %q, want unchanged %q", got, "original")
		}
		if got := mustRead(t, b, "other.png"); got != "incoming" {
			t.Errorf("source must still be present after a refused rename, content = %q", got)
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
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", name, err)
	}
	return string(data)
}
