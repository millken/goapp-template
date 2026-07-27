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
				"dist": func() { eng.StaticFS("/", dist) },
				// StaticFileServer trims prefix against the raw request path, so
				// the trailing slash is required here for the same reason it is
				// required in commands/serve.go's real wiring.
				"uploads": func() { eng.GET("/uploads/*", inertia.StaticFileServer("/uploads/", uploads)) },
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
