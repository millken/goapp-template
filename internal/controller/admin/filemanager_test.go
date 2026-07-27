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
