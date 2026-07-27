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

// uploadsEngine wires up the real storage service on a temp root, the same
// way runServe does: two routes over one storageFileServer, at the bare
// prefix and the wildcard.
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
	serve := storageFileServer(prefix, stor.FS())
	eng.GET(prefix, serve)
	eng.GET(prefix+"*", serve)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("registration: %v", err)
	}
	return eng, stor
}

// TestStorageFileServer_MalformedPathIs404NotAServerError is finding 4:
// inertia.StaticFileServer trims its prefix off the decoded r.URL.Path and
// hands the rest to fs.FS.Open, which answers fs.ErrInvalid for a ".."
// segment — neither os.IsNotExist nor os.IsPermission, so unwrapped this
// falls through to inertia's 500 handler and an ERROR log line for every
// malformed request an unauthenticated scanner sends. Every case here must
// answer 404 instead, the same as a path that is merely missing.
func TestStorageFileServer_MalformedPathIs404NotAServerError(t *testing.T) {
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

// TestStorageFileServer_ServesAnActualFile guards against the fix answering
// 404 for everything: a real, legal path must still resolve, and a merely
// missing one (as opposed to a malformed one) must still be a plain 404.
func TestStorageFileServer_ServesAnActualFile(t *testing.T) {
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
