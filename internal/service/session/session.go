// Package session is the request-scoped session infrastructure service.
//
// Middleware loads (or creates) a session per request and puts it on the
// request's context.Context; serve.go installs it globally. The session ID
// travels in a signed cookie (HMAC-SHA256); the data lives in a pluggable Store
// (memory for development, db for production). It implements app.Lifecycle.
package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/inertia"
)

// sessionCtxKey is the request-context key holding the active Session.
//
// The session rides on the request's context.Context rather than on the
// inertia.Context (c.Set) because that map is not scratch space: Render
// serializes it wholesale as the page props, so anything parked there is shipped
// to the browser. Props are for the page; request-scoped state is not.
type sessionCtxKey struct{}

// Provider exposes the per-request session; consumers depend on this interface.
type Provider interface {
	Session(c *inertia.Context) Session
}

// StoreKind selects the backing store.
type StoreKind string

const (
	StoreMemory StoreKind = "memory" // default; development
	StoreDB     StoreKind = "db"     // production; requires a non-nil *sqldb.DB
)

// Config configures the session service.
type Config struct {
	Secret     string        `yaml:"secret"`      // HMAC key; required
	CookieName string        `yaml:"cookie_name"` // default "session"
	TTL        time.Duration `yaml:"ttl"`         // default 24h
	Store      StoreKind     `yaml:"store"`       // memory | db
	DBTable    string        `yaml:"db_table"`    // sessions table name when store=db; default "sessions"
	Secure     bool          `yaml:"secure"`      // cookie Secure flag (HTTPS-only)
	SameSite   string        `yaml:"same_site"`   // lax | strict | none (default lax)
	Path       string        `yaml:"path"`        // default "/"
	Domain     string        `yaml:"domain"`
}

// Service is the session infrastructure service.
type Service struct {
	cfg   *Config
	store Store
	db    *sqldb.DB
}

// New constructs the session service. db is used only when Store=db and may be
// nil otherwise — passing nil is how a db-less build falls back to the memory
// store. It takes the handle rather than a provider interface so this package
// depends on no other component; the caller passes the post-Start handle
// (app.Services.DB), which is why the store is resolved in Start.
func New(cfg *Config, db *sqldb.DB) *Service {
	return &Service{cfg: cfg, db: db}
}

// Start resolves the store. For store=db the db service must Start first.
func (s *Service) Start(ctx context.Context) error {
	if s.cfg == nil {
		return errors.New("session: service enabled but [session] config section missing")
	}
	if s.cfg.Secret == "" {
		return errors.New("session: secret is required")
	}

	switch s.cfg.Store {
	case StoreDB:
		if s.db == nil {
			return errors.New("session: store=db but no database handle (is the db component enabled and Started first?)")
		}
		store, err := NewDBStore(s.db, s.cfg.DBTable)
		if err != nil {
			return err
		}
		if err := store.ensureTable(ctx); err != nil {
			return err
		}
		s.store = store
	case StoreMemory, "":
		s.store = NewMemoryStore()
	default:
		return fmt.Errorf("session: unknown store %q", s.cfg.Store)
	}
	return nil
}

// Middleware returns the HandlerFunc that loads/creates the session per request.
// It injects the response writer so Save/Destroy can emit the cookie
// synchronously — inertia's writer is write-through, so a cookie set after the
// handler renders would be dropped.
func (s *Service) Middleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		sess := s.loadOrCreate(c.Request.Context(), c.Request)
		sess.w = c.Writer
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), sessionCtxKey{}, sess))

		// Unsafe methods carry a token or they do not run. The check is here
		// rather than in its own middleware because this one already holds the
		// session and is already mounted once, globally.
		//
		// 403 with a plain body, not a redirect: a redirect re-renders the form
		// and reads as a validation problem, and this is not one.
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !sess.validCSRF(c.Request) {
				slog.Warn("session: csrf check failed",
					"method", c.Request.Method, "path", c.Request.URL.Path)
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}

		// Consume any staged flash before the handler runs: the removal has to be
		// persisted or the message repeats forever, and saving here lands the
		// cookie ahead of the body flush.
		if flash := sess.takeFlash(); len(flash) > 0 {
			if _, err := sess.Save(c.Request.Context()); err != nil {
				// The store still holds the flash. Withholding it costs one
				// delayed message; injecting it would repeat it on every request
				// until the store recovers.
				slog.Warn("session: persisting flash consumption failed", "err", err)
			} else {
				c.Set("flash", flash)
			}
		}

		c.Next()
	}
}

// loadOrCreate verifies the signed cookie and loads the session, or returns a
// fresh empty one if absent or invalid.
func (s *Service) loadOrCreate(ctx context.Context, r *http.Request) *session {
	if cookie, err := r.Cookie(s.cookieName()); err == nil {
		if id, err := verifyCookie(s.cfg.Secret, cookie.Value); err == nil {
			values, _, ok, err := s.store.Load(ctx, id)
			if err != nil {
				slog.Warn("session load failed", "err", err)
			} else if ok {
				return &session{id: id, values: values, mod: s}
			}
		}
	}
	return &session{values: make(map[string]any), mod: s}
}

// Session returns the active session for the request, panicking if the
// middleware did not run for it.
func (s *Service) Session(c *inertia.Context) Session {
	sess, ok := c.Request.Context().Value(sessionCtxKey{}).(*session)
	if !ok {
		panic("session: Session() called but middleware did not run for this request")
	}
	return sess
}

// Stop is a no-op; stores own no resources (db connections belong to the db service).
func (s *Service) Stop(context.Context) error { return nil }

// cookieName returns the configured cookie name or the default.
func (s *Service) cookieName() string {
	return cmp.Or(s.cfg.CookieName, "session")
}

// ttl returns the configured TTL or the default.
func (s *Service) ttl() time.Duration {
	if s.cfg.TTL > 0 {
		return s.cfg.TTL
	}
	return 24 * time.Hour
}

// sameSite maps the config string to http.SameSite.
func (s *Service) sameSite() http.SameSite {
	switch s.cfg.SameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

// setCookie writes the signed session cookie. HttpOnly is always on.
func (s *Service) setCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    signCookie(s.cfg.Secret, id),
		Path:     cmp.Or(s.cfg.Path, "/"),
		Domain:   s.cfg.Domain,
		MaxAge:   int(s.ttl().Seconds()),
		Secure:   s.cfg.Secure,
		HttpOnly: true,
		SameSite: s.sameSite(),
	})
}

// clearCookie expires the session cookie.
func (s *Service) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     cmp.Or(s.cfg.Path, "/"),
		Domain:   s.cfg.Domain,
		MaxAge:   -1,
		Secure:   s.cfg.Secure,
		HttpOnly: true,
		SameSite: s.sameSite(),
	})
}
