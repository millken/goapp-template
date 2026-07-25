package session

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"

	"github.com/millken/inertia"
)

// eachStore runs fn against both backing stores. The flat-string key design
// exists precisely so memory and db agree — memory keeps native Go values while
// db round-trips through JSON (store_db.go) — so every delivery test runs twice.
//
// The SQLite driver is registered by store_test.go's blank import: same package,
// same test binary.
func eachStore(t *testing.T, fn func(t *testing.T, cfg *Config, db *sqldb.DB)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, &Config{Secret: "flash-secret", Store: StoreMemory}, nil)
	})
	t.Run("db", func(t *testing.T) {
		db, err := sqldb.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		fn(t, &Config{Secret: "flash-secret", Store: StoreDB, DBTable: "sessions_flash_test"}, db)
	})
}

// installedWith is installed() with a database handle, for the store=db leg.
func installedWith(t *testing.T, eng *inertia.Engine, cfg *Config, db *sqldb.DB) *Service {
	t.Helper()
	svc := New(cfg, db)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eng.Use(svc.Middleware())
	return svc
}

// flashProbe registers a route that records the flash prop the middleware
// injected, so tests assert on the prop rather than on any render payload.
func flashProbe(eng *inertia.Engine, path string, got *any, ok *bool) {
	eng.GET(path, func(c *inertia.Context) {
		*got, *ok = c.Get("flash")
	})
}

// TestFlash_DeliveredExactlyOnce is the read-once guarantee: the request after
// the flash sees it, the one after that does not.
func TestFlash_DeliveredExactlyOnce(t *testing.T) {
	eachStore(t, func(t *testing.T, cfg *Config, db *sqldb.DB) {
		eng := newTestEngine(t)
		svc := installedWith(t, eng, cfg, db)

		eng.POST("/save", func(c *inertia.Context) {
			s := svc.Session(c)
			s.Flash("success", "Post created")
			if _, err := s.Save(c.Request.Context()); err != nil {
				t.Errorf("Save: %v", err)
			}
		})

		var got any
		var ok bool
		flashProbe(eng, "/page", &got, &ok)

		w1 := httptest.NewRecorder()
		eng.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/save", nil))
		cookies := w1.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("expected the flash Save to set a cookie, got %d", len(cookies))
		}
		signed := cookies[0].Value

		// First read: the prop is there.
		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodGet, "/page", nil)
		r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
		eng.ServeHTTP(w2, r2)

		if !ok {
			t.Fatal("expected the flash prop on the request after the flash")
		}
		msgs, isMap := got.(map[string]string)
		if !isMap {
			t.Fatalf("flash prop = %T, want map[string]string", got)
		}
		if msgs["success"] != "Post created" {
			t.Errorf(`flash["success"] = %q, want "Post created"`, msgs["success"])
		}

		// Second read: gone.
		got, ok = nil, false
		w3 := httptest.NewRecorder()
		r3 := httptest.NewRequest(http.MethodGet, "/page", nil)
		r3.AddCookie(&http.Cookie{Name: "session", Value: signed})
		eng.ServeHTTP(w3, r3)

		if ok {
			t.Fatalf("expected the flash to be consumed, got it again: %v", got)
		}
	})
}

// TestFlash_MultipleKinds verifies kinds coexist and the storage prefix is
// stripped on the way out.
func TestFlash_MultipleKinds(t *testing.T) {
	eachStore(t, func(t *testing.T, cfg *Config, db *sqldb.DB) {
		eng := newTestEngine(t)
		svc := installedWith(t, eng, cfg, db)

		eng.POST("/save", func(c *inertia.Context) {
			s := svc.Session(c)
			s.Flash("success", "Saved")
			s.Flash("error", "Thumbnail failed")
			if _, err := s.Save(c.Request.Context()); err != nil {
				t.Errorf("Save: %v", err)
			}
		})

		var got any
		var ok bool
		flashProbe(eng, "/page", &got, &ok)

		w1 := httptest.NewRecorder()
		eng.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/save", nil))
		signed := w1.Result().Cookies()[0].Value

		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodGet, "/page", nil)
		r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
		eng.ServeHTTP(w2, r2)

		msgs, _ := got.(map[string]string)
		if len(msgs) != 2 {
			t.Fatalf("flash prop = %v, want 2 entries keyed by kind", got)
		}
		if msgs["success"] != "Saved" || msgs["error"] != "Thumbnail failed" {
			t.Errorf("flash prop = %v, want success/error messages with the prefix stripped", msgs)
		}
	})
}

// TestFlash_SessionValuesStayHidden guards the reserved keys from leaking: a
// consumer reading the session sees no _flash: entries, before or after delivery.
func TestFlash_SessionValuesStayHidden(t *testing.T) {
	eachStore(t, func(t *testing.T, cfg *Config, db *sqldb.DB) {
		eng := newTestEngine(t)
		svc := installedWith(t, eng, cfg, db)

		eng.POST("/save", func(c *inertia.Context) {
			s := svc.Session(c)
			s.Flash("success", "Saved")
			_, _ = s.Save(c.Request.Context())
		})

		var raw any
		var rawOK bool
		eng.GET("/page", func(c *inertia.Context) {
			raw, rawOK = svc.Session(c).Get(flashPrefix + "success")
		})

		w1 := httptest.NewRecorder()
		eng.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/save", nil))
		signed := w1.Result().Cookies()[0].Value

		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodGet, "/page", nil)
		r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
		eng.ServeHTTP(w2, r2)

		if rawOK {
			t.Fatalf("expected the reserved flash key to be removed from session values, got %v", raw)
		}
	})
}

// TestFlash_NoSaveWhenNoFlash is the zero-cost claim: a request carrying a
// session that holds no flash must not write. A Save would re-emit the cookie,
// so the absent Set-Cookie is the observable proxy for "no store write".
//
// Distinct from TestMiddleware_NoCookieWhenSessionUntouched, which covers a
// request with no session at all.
func TestFlash_NoSaveWhenNoFlash(t *testing.T) {
	eachStore(t, func(t *testing.T, cfg *Config, db *sqldb.DB) {
		eng := newTestEngine(t)
		svc := installedWith(t, eng, cfg, db)

		eng.POST("/login", func(c *inertia.Context) {
			s := svc.Session(c)
			s.Set("user", "alice") // a real session, but no flash
			_, _ = s.Save(c.Request.Context())
		})
		eng.GET("/page", func(c *inertia.Context) {
			_ = svc.Session(c)
		})

		w1 := httptest.NewRecorder()
		eng.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/login", nil))
		signed := w1.Result().Cookies()[0].Value

		w2 := httptest.NewRecorder()
		r2 := httptest.NewRequest(http.MethodGet, "/page", nil)
		r2.AddCookie(&http.Cookie{Name: "session", Value: signed})
		eng.ServeHTTP(w2, r2)

		if n := len(w2.Result().Cookies()); n != 0 {
			t.Fatalf("expected no write for a flash-less session, got %d cookie(s)", n)
		}
	})
}

// unwritableStore loads a staged flash but fails every Save, standing in for a
// database that has gone away mid-request.
type unwritableStore struct {
	values map[string]any
}

func (s *unwritableStore) Load(context.Context, string) (map[string]any, time.Time, bool, error) {
	// Copy, as both real stores do — so the test can tell whether the flash
	// survived in the store rather than observing its own mutations.
	out := make(map[string]any, len(s.values))
	maps.Copy(out, s.values)
	return out, time.Now().Add(time.Hour), true, nil
}

func (s *unwritableStore) Save(context.Context, string, map[string]any, time.Duration) (string, error) {
	return "", errors.New("store is down")
}

func (s *unwritableStore) Delete(context.Context, string) error { return nil }

// TestFlash_SaveFailureSuppressesProp is the at-most-once guarantee. If the
// removal cannot be persisted, showing the message now would show it again on
// every later request, so it must be withheld and left in the store to retry.
func TestFlash_SaveFailureSuppressesProp(t *testing.T) {
	eng := newTestEngine(t)
	svc := installed(t, eng, &Config{Secret: "flash-secret", Store: StoreMemory})

	store := &unwritableStore{values: map[string]any{flashPrefix + "success": "Post created"}}
	svc.store = store

	var got any
	var ok bool
	flashProbe(eng, "/page", &got, &ok)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/page", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: signCookie("flash-secret", "sid")})
	eng.ServeHTTP(w, r)

	if ok {
		t.Fatalf("expected no flash prop when the removal could not be persisted, got %v", got)
	}
	if store.values[flashPrefix+"success"] != "Post created" {
		t.Error("expected the flash to remain in the store so a later request can retry it")
	}
}
