package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// uploadsEngine wires up the real storage service on a temp root, the same way
// runServe does: one wildcard route over inertia.StaticFileServer.
func uploadsEngine(t *testing.T) (*inertia.Engine, *storage.Service) {
	t.Helper()
	stor := storage.New(&storage.Config{Root: t.TempDir()})
	if err := stor.Start(context.Background()); err != nil {
		t.Fatalf("start storage: %v", err)
	}
	t.Cleanup(func() { _ = stor.Stop(context.Background()) })

	eng, err := inertia.New(inertia.WithMode(inertia.ModeProduction))
	if err != nil {
		t.Fatalf("inertia.New: %v", err)
	}
	prefix := stor.URLPrefix() + "/"
	eng.GET(prefix+"*", inertia.StaticFileServer(prefix, stor.FS()))
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("registration: %v", err)
	}
	return eng, stor
}

// TestUploadsRoute_MalformedPathIs404NotAServerError pins the two ways this
// route used to answer 500 for ordinary traffic. Both were fixed in inertia
// v1.1.4 and the wrapper this file used to test is gone, but the guarantee is
// the storage area's, not the router's, so it stays pinned here: a ".."
// segment reaches fs.FS.Open as fs.ErrInvalid (neither ErrNotExist nor
// ErrPermission), and "/uploads/" is a tree node with no handler of its own.
// Every case must be a 404, the same as a path that is merely missing.
func TestUploadsRoute_MalformedPathIs404NotAServerError(t *testing.T) {
	eng, _ := uploadsEngine(t)
	for _, p := range []string{
		"/uploads/../secret.txt",
		"/uploads/..%2fsecret.txt",
		"/uploads/%2e%2e%2fsecret.txt",
		"/uploads/",
	} {
		t.Run(p, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			eng.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d %q, want 404", p, w.Code, w.Body.String())
			}
		})
	}
}

// TestUploadsRoute_ServesAnActualFile guards against the fix answering
// 404 for everything: a real, legal path must still resolve, and a merely
// missing one (as opposed to a malformed one) must still be a plain 404.
func TestUploadsRoute_ServesAnActualFile(t *testing.T) {
	eng, stor := uploadsEngine(t)
	if _, err := stor.Upload(context.Background(), "", "a.png", strings.NewReader("x")); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/uploads/a.png", nil)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "x" {
		t.Errorf("GET /uploads/a.png = %d %q, want 200 %q", w.Code, w.Body.String(), "x")
	}

	r = httptest.NewRequest(http.MethodGet, "/uploads/nope.png", nil)
	w = httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /uploads/nope.png = %d, want 404 (merely missing, not malformed)", w.Code)
	}
}
