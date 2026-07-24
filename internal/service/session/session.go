// Package session is an infrastructure service providing request-scoped
// sessions.
//
// It exposes Middleware() — a HandlerFunc that loads (or creates) a session per
// request and stores it on the inertia.Context for handlers to use via the
// Provider. serve.go installs it globally with eng.Use(sessSvc.Middleware()).
// The session ID travels in a signed cookie (HMAC-SHA256); the session data
// lives in a pluggable Store (memory for development, db for production).
//
// It implements app.Lifecycle (Start resolves the store, Stop is a no-op) and
// imports no app kernel.
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

// contextKey is the inertia.Context key under which the active Session is stored
// for the duration of a request.
const contextKey = "session"

// Provider exposes the per-request session. Consumers (e.g. admin) depend on
// this interface and call Session lazily from handlers.
type Provider interface {
	Session(c *inertia.Context) Session
}

// DBProvider is the dependency on the db service. It is satisfied by
// *db.Service (which implements db.Provider with the same signature) without
// session importing the db package.
type DBProvider interface {
	DB() *sqldb.DB
}

// StoreKind selects the backing store.
type StoreKind string

const (
	StoreMemory StoreKind = "memory" // default; development
	StoreDB     StoreKind = "db"     // production; requires DBProvider
)

// Config configures the session service. Pointer in New so Start can enforce the
// enable-consistency rule.
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
	cfg    *Config
	store  Store
	dbProv DBProvider
}

// New constructs the session service. dbProv is required only when Store=db; it
// may be nil otherwise. The store is resolved in Start (db needs the handle,
// which is only valid after db Starts — so construction is deferred to Start).
func New(cfg *Config, dbProv DBProvider) *Service {
	return &Service{cfg: cfg, dbProv: dbProv}
}

// Start resolves the store. For store=db it needs the db handle, so the db
// service must Start first (Start db before session in serve.go).
func (s *Service) Start(ctx context.Context) error {
	if s.cfg == nil {
		return errors.New("session: service enabled but [session] config section missing")
	}
	if s.cfg.Secret == "" {
		return errors.New("session: secret is required")
	}

	switch s.cfg.Store {
	case StoreDB:
		if s.dbProv == nil {
			return errors.New("session: store=db requires a db.Provider")
		}
		store, err := NewDBStore(s.dbProv.DB(), s.cfg.DBTable)
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
// It injects the response writer into the session so Save/Destroy can emit the
// cookie synchronously (inertia's writer is write-through; a cookie set after
// the handler renders would be dropped — see impl.go). serve.go installs it via
// eng.Use.
func (s *Service) Middleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		sess := s.loadOrCreate(c.Request.Context(), c.Request)
		sess.w = c.Writer
		c.Set(contextKey, sess)
		c.Next()
	}
}

// loadOrCreate reads the signed cookie, verifies it, and loads the session from
// the store; if absent or invalid it returns a fresh empty session.
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
// middleware did not run (i.e. the route was registered without the session
// middleware, or the handler is called outside a request).
func (s *Service) Session(c *inertia.Context) Session {
	v, ok := c.Get(contextKey)
	if !ok {
		panic("session: Session() called but middleware did not run for this request")
	}
	sess, ok := v.(*session)
	if !ok {
		panic("session: stored session has unexpected type")
	}
	return sess
}

// Stop is a no-op; stores own no resources that need releasing at shutdown (db
// connections are owned by the db service).
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

// setCookie writes the signed session cookie on the response. HttpOnly is
// always on — there is no opt-out; if one is ever needed, add an explicit field
// rather than a bool whose zero value is ambiguous.
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
