package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/service/session"
	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// fmStack is loginStack plus a started storage service on a temp directory.
//
// loginStack's Mount already registered the file manager's routes, so this
// only fills the field they read. Mounting again here would be a duplicate
// registration, which is what the engine's RegistrationError below would
// report.
func fmStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	stor := storage.New(&storage.Config{Root: t.TempDir()})
	if err := stor.Start(context.Background()); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(context.Background()) })
	adm.Storage = stor
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

func TestFileManagerUpload_ABackendFailureIsAnItemErrorNotABatchAbort(t *testing.T) {
	// This is the assertion the fmFail-abort regressed: a per-file backend
	// error (here, the destination refusing writes) must be reported per
	// item and must not truncate the batch or turn the response into a 500,
	// exactly like storage.Move and storage.Delete already behave.
	eng, adm := loginStack(t)

	// fmStack hides the storage root inside t.TempDir(); this test needs the
	// path itself so it can chmod it, so the setup is inlined rather than
	// reused.
	root := t.TempDir()
	stor := storage.New(&storage.Config{Root: root})
	if err := stor.Start(context.Background()); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(context.Background()) })
	adm.Storage = stor
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	cookie := loginAndGetCookie(t, eng)

	// A root process ignores directory permission bits, so the Create below
	// would silently succeed and this test would assert nothing.
	if os.Geteuid() == 0 {
		t.Fatal("must not run as root: permission bits would not be enforced")
	}

	// Read+execute but no write: freeName's Stat/List still succeed, so the
	// service gets as far as the backend's Create, which then fails on every
	// file with a permission error — a backend failure that is neither
	// storage.ErrBadPath nor storage.ErrRejected.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	body, ctype := uploadBody(t, map[string]string{
		"a.png": "one",
		"b.png": "two",
		"c.png": "three",
	})
	code, out := postUpload(t, eng, cookie, "/admin/filemanager/api/upload", body, ctype)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even though every file failed; body: %+v", code, out)
	}
	entries, _ := out["entries"].([]any)
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none: the destination accepts no writes", out["entries"])
	}
	fails, _ := out["errors"].([]any)
	if len(fails) != 3 {
		t.Fatalf("errors = %v, want all three files reported — a truncated batch stops short of this", out["errors"])
	}
	for _, f := range fails {
		fe, _ := f.(map[string]any)
		if fe["error"] == "" || fe["error"] == nil {
			t.Errorf("item error missing a reason: %+v", fe)
		}
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
