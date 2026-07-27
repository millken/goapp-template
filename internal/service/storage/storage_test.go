package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
		"":             "",
		"a.png":        "a.png",
		"photos":       "photos",
		"photos/a.png": "photos/a.png",
		"图片/一.png":     "图片/一.png",
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

// TestSymlinkEscapeIsRefusedByStatOpenAndFS is §7.2's symlink case: a symlink
// planted inside the tree that points outside it. "escape.txt" is a perfectly
// legal path per clean() — the string-cleaning layer has nothing to catch
// here — so this asserts the *other* layer, os.Root, refuses at the syscall.
// It exists to fail if someone later swaps os.OpenRoot for os.DirFS, or a
// root.Open for an os.Open: see the mutation check recorded in
// .superpowers/sdd/final-go-report.md, which did exactly that, temporarily,
// and watched this test go red before reverting.
func TestSymlinkEscapeIsRefusedByStatOpenAndFS(t *testing.T) {
	base := t.TempDir()
	rootDir := filepath.Join(base, "root")
	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not serve this"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(rootDir, "escape.txt")); err != nil {
		t.Fatal(err)
	}

	s := New(&Config{Root: rootDir})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	ctx := context.Background()

	if _, err := s.Stat(ctx, "escape.txt"); err == nil {
		t.Error("Stat must refuse a symlink pointing outside the root")
	}
	if _, err := s.be.Open(ctx, "escape.txt"); err == nil {
		t.Error("Backend.Open must refuse a symlink pointing outside the root")
	}
	// Service.FS() is what the public /uploads/* route actually serves
	// through, so this is the one that matters most: a symlink escape here is
	// a symlink escape onto the internet.
	if f, err := s.FS().Open("escape.txt"); err == nil {
		f.Close()
		t.Error("Service.FS() must refuse a symlink pointing outside the root")
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

// TestUpload_AllowedExtIsCaseInsensitive covers an operator config of
// allowed_ext: [".PNG"]: Upload compares against a lowercased extension, so
// without lowercasing the accessor too, this would reject every .png upload
// with a message naming the very extension the operator just allowed.
func TestUpload_AllowedExtIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	s.cfg.AllowedExt = []string{".PNG"}
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Errorf("allowed_ext: [\".PNG\"] must still accept a.png, got %v", err)
	}
}

// TestUpload_ADotfileGetsAnHonestRejectionMessage covers sanitiseFilename's
// side effect on a leading-dot name: sanitiseFilename(".png") is "png", not
// ".png" — the leading dot is trimmed along with the rest, so the extension
// check sees no extension at all. Rejecting it is right; blaming an empty
// extension ("") for it was not.
func TestUpload_ADotfileGetsAnHonestRejectionMessage(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	_, err := s.Upload(ctx, "", ".png", strings.NewReader("x"))
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("a dotfile with no extension left must be rejected, got %v", err)
	}
	if strings.Contains(err.Error(), `""`) {
		t.Errorf("message = %q, must not blame an empty extension it never had", err.Error())
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

// fakeSharedListBackend returns the same backing slice from every List call,
// the way a backend that caches its listing would — unlike localBackend,
// which happens to build a fresh slice per call. Browse must not rely on that
// happenstance.
type fakeSharedListBackend struct{ entries []Entry }

func (f *fakeSharedListBackend) List(context.Context, string) ([]Entry, error) {
	return f.entries, nil
}
func (f *fakeSharedListBackend) Stat(context.Context, string) (Entry, error) {
	return Entry{}, fs.ErrNotExist
}
func (f *fakeSharedListBackend) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, fs.ErrNotExist
}
func (f *fakeSharedListBackend) Save(context.Context, string, io.Reader) error { return nil }
func (f *fakeSharedListBackend) Mkdir(context.Context, string) error           { return nil }
func (f *fakeSharedListBackend) Rename(context.Context, string, string) error  { return nil }
func (f *fakeSharedListBackend) Remove(context.Context, string) error          { return nil }

// TestBrowse_DoesNotMutateTheBackendsSlice guards an unstated obligation: a
// filtered Browse used to run slices.DeleteFunc directly on whatever List
// returned. Safe for localBackend, which builds a fresh slice every call, but
// a backend that hands back a slice it intends to reuse would see Browse
// corrupt its own listing.
func TestBrowse_DoesNotMutateTheBackendsSlice(t *testing.T) {
	be := &fakeSharedListBackend{entries: []Entry{{Name: "a.png"}, {Name: "b.png"}, {Name: "keep.png"}}}
	want := slices.Clone(be.entries)
	s := &Service{cfg: &Config{}, be: be}
	if _, err := s.Browse(context.Background(), "", "keep", 1); err != nil {
		t.Fatalf("Browse: %v", err)
	}
	// slices.DeleteFunc compacts kept elements to the front of the backing
	// array in place: the returned (shorter) slice header looks fine, but
	// be.entries — same backing array, unchanged length — would still read
	// back the compacted, now-wrong contents. A length check alone would not
	// catch this: DeleteFunc never changes the caller's own slice header.
	if !slices.Equal(be.entries, want) {
		t.Errorf("backend's own slice was corrupted by a filtered Browse: got %+v, want %+v", be.entries, want)
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

func TestRename_RootLevelFile(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "a.png", "b.png"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := s.Stat(ctx, "b.png"); err != nil {
		t.Errorf("the renamed file must exist at the root: %v", err)
	}
	if _, err := s.Stat(ctx, "a.png"); err == nil {
		t.Error("the old name must be gone")
	}
}

func TestRename_StaysInItsSubdirectory(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if err := s.Mkdir(ctx, "", "sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "sub", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "sub/a.png", "b.png"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := s.Stat(ctx, "sub/b.png"); err != nil {
		t.Errorf("the renamed file must stay in its own directory: %v", err)
	}
	if _, err := s.Stat(ctx, "b.png"); err == nil {
		t.Error("a rename must not smuggle a move to the root")
	}
}

func TestRename_Directory(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if err := s.Mkdir(ctx, "", "old"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "old", "new"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if e, err := s.Stat(ctx, "new"); err != nil || !e.IsDir {
		t.Errorf("renamed directory: entry = %+v, err = %v", e, err)
	}
	if _, err := s.Stat(ctx, "old"); err == nil {
		t.Error("the old name must be gone")
	}
}

func TestRename_RefusesANewNameWithASeparator(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "a.png", "../escape.png"); !errors.Is(err, ErrBadPath) {
		t.Errorf("a newName that escapes must be refused, got %v", err)
	}
	if err := s.Rename(ctx, "a.png", "sub/b.png"); !errors.Is(err, ErrBadPath) {
		t.Errorf("a newName with a separator would make rename a silent move; got %v", err)
	}
}

// TestRename_RefusesAnExistingDestination is the Service-level half of
// finding 1: Rename and Move used to hand straight to Backend.Rename, and
// os.Root.Rename is renameat(2), which overwrites silently. reason() already
// mapped fs.ErrExist to a Chinese message and fmFail already mapped it to
// 409 — both dead code until this refusal exists.
func TestRename_RefusesAnExistingDestination(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "keep.png", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "", "other.png", strings.NewReader("incoming")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "other.png", "keep.png"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Rename onto an existing name: err = %v, want fs.ErrExist", err)
	}
	f, err := s.be.Open(ctx, "keep.png")
	if err != nil {
		t.Fatalf("Open(keep.png): %v", err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != "original" {
		t.Errorf("keep.png content = %q, want unchanged %q", got, "original")
	}
	if _, err := s.Stat(ctx, "other.png"); err != nil {
		t.Error("a refused rename must leave the source in place")
	}
}

// TestRename_ToItsOwnCurrentNameIsANoOp guards the edge the collision check
// introduces: submitting a rename with no actual change must not make the
// destination collide with itself.
func TestRename_ToItsOwnCurrentNameIsANoOp(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "a.png", "a.png"); err != nil {
		t.Errorf("renaming a file to its own current name must succeed as a no-op, got %v", err)
	}
}

// TestMove_RefusesACollidingItemButMovesTheRest is Move's per-item half of
// finding 1: one colliding name must not sink the rest of the batch.
func TestMove_RefusesACollidingItemButMovesTheRest(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if err := s.Mkdir(ctx, "", "dst"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "dst", "a.png", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "", "a.png", strings.NewReader("incoming")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "", "b.png", strings.NewReader("y")); err != nil {
		t.Fatal(err)
	}

	fails, err := s.Move(ctx, []string{"a.png", "b.png"}, "dst")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(fails) != 1 || fails[0].Name != "a.png" {
		t.Fatalf("fails = %+v, want exactly a.png", fails)
	}
	f, err := s.be.Open(ctx, "dst/a.png")
	if err != nil {
		t.Fatalf("Open(dst/a.png): %v", err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != "original" {
		t.Errorf("dst/a.png content = %q, want unchanged %q", got, "original")
	}
	if _, err := s.Stat(ctx, "a.png"); err != nil {
		t.Error("the colliding source must stay put, not be consumed by the refused move")
	}
	if _, err := s.Stat(ctx, "dst/b.png"); err != nil {
		t.Errorf("the non-colliding item must still have moved: %v", err)
	}
}

func TestRename_RefusesTheRoot(t *testing.T) {
	if err := startedService(t).Rename(context.Background(), "", "whatever"); !errors.Is(err, ErrBadPath) {
		t.Errorf("renaming the root must be refused, got %v", err)
	}
}

func TestURLFor(t *testing.T) {
	s := startedService(t)
	if got := s.URLFor("a/b.png"); got != "/uploads/a/b.png" {
		t.Errorf("URLFor = %q", got)
	}
}

// TestURLFor_EscapesPerSegment covers the characters sanitiseFilename
// deliberately keeps (#, %, ?) but that are meaningful in a URL: "#" starts a
// fragment, "?" starts a query, and a bare "%" is not even parseable —
// httptest.NewRequest panics on "/uploads/100%.png". A space cannot survive
// sanitiseFilename (it becomes "-"), but ValidatePath accepts one directly —
// avatar and other stored-path fields do not go through sanitisation — so a
// path with an embedded space is still a real input to URLFor.
func TestURLFor_EscapesPerSegment(t *testing.T) {
	s := startedService(t)
	cases := map[string]string{
		"photo#1.png": "/uploads/photo%231.png",
		"100%.png":    "/uploads/100%25.png",
		"a?b.png":     "/uploads/a%3Fb.png",
		"a b.png":     "/uploads/a%20b.png",
		"dir/a#b.png": "/uploads/dir/a%23b.png",
	}
	for in, want := range cases {
		got := s.URLFor(in)
		if got != want {
			t.Errorf("URLFor(%q) = %q, want %q", in, got, want)
		}
		if _, err := url.Parse(got); err != nil {
			t.Errorf("URLFor(%q) = %q, not a parseable URL: %v", in, got, err)
		}
	}
}

// TestURLFor_ChineseFilenameRoundTripsThroughTheStaticRoute proves the escape
// does not go too far: url.PathEscape percent-encodes every non-ASCII byte,
// so it is worth confirming a legitimate Chinese filename still resolves
// rather than becoming technically-valid-but-unreachable. The check mirrors
// what the static route actually does (inertia.StaticFileServer): trim the
// prefix off the request's already-decoded URL.Path and hand the rest to
// fs.FS.Open — so parsing the URL back and reopening through s.FS() is the
// real round trip, not a proxy for it.
func TestURLFor_ChineseFilenameRoundTripsThroughTheStaticRoute(t *testing.T) {
	ctx := context.Background()
	s := startedService(t)
	if _, err := s.Upload(ctx, "", "图片.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}

	got := s.URLFor("图片.png")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("URLFor(%q) = %q, not a parseable URL: %v", "图片.png", got, err)
	}
	// url.Parse already unescapes into u.Path, exactly as net/http does for
	// an incoming request's r.URL.Path.
	reqPath := strings.TrimPrefix(u.Path, s.URLPrefix()+"/")
	f, err := s.FS().Open(reqPath)
	if err != nil {
		t.Fatalf("the static route could not open what URLFor(%q) pointed at (%q): %v", "图片.png", got, err)
	}
	f.Close()
}
