// Package session is a feature module providing request-scoped sessions.
//
// It is an app.Module whose Register installs middleware that loads (or
// creates) a session per request and stores it on the inertia.Context for
// handlers to use via the Provider. The session ID travels in a signed cookie
// (HMAC-SHA256); the session data lives in a pluggable Store (memory for
// development, db for production).
//
// Ordering contract (§4.2): register session before any module whose handlers
// depend on it (e.g. admin). Because inertia composes the middleware chain at
// request time in registration order, session's middleware wraps those handlers.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/app"
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

// DBProvider is the dependency on the db module. It is satisfied by
// *db.Module (which implements db.Provider with the same signature) without
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

// Config configures the session module. Pointer in New so Boot can enforce the
// enable-consistency rule (§4.4).
type Config struct {
	Secret     string        `yaml:"secret"`      // HMAC key; required
	CookieName string        `yaml:"cookie_name"` // default "session"
	TTL        time.Duration `yaml:"ttl"`         // default 24h
	Store      StoreKind     `yaml:"store"`       // memory | db
	DBTable    string        `yaml:"db_table"`    // sessions table name when store=db; default "sessions"
	Secure     bool          `yaml:"secure"`      // cookie Secure flag (HTTPS-only)
	SameSite   string        `yaml:"same_site"`   // lax | strict | none (default lax)
	HttpOnly   bool          `yaml:"http_only"`   // default true (zero value means true)
	Path       string        `yaml:"path"`        // default "/"
	Domain     string        `yaml:"domain"`
}

// Module is the session feature module.
type Module struct {
	cfg    *Config
	store  Store
	dbProv DBProvider
}

// New constructs the session module. dbProv is required only when Store=db; it
// may be nil otherwise. The store is resolved in Boot (db needs the handle,
// which is only valid after db Boots — so construction is deferred to Boot).
func New(cfg *Config, dbProv DBProvider) *Module {
	return &Module{cfg: cfg, dbProv: dbProv}
}

// Register installs the session middleware on the engine.
func (m *Module) Register(a *app.App) error {
	a.Engine.Use(m.middleware())
	return nil
}

// Boot resolves the store. For store=db it needs the db handle, so the db module
// must Boot first (place session after db in app.Use).
func (m *Module) Boot(ctx context.Context) error {
	if m.cfg == nil {
		return errors.New("session: module enabled but [session] config section missing")
	}
	if m.cfg.Secret == "" {
		return errors.New("session: secret is required")
	}

	switch m.cfg.Store {
	case StoreDB:
		if m.dbProv == nil {
			return errors.New("session: store=db requires a db.Provider")
		}
		store := NewDBStore(m.dbProv.DB(), m.cfg.DBTable)
		if err := store.ensureTable(ctx); err != nil {
			return err
		}
		m.store = store
	case StoreMemory, "":
		m.store = NewMemoryStore()
	default:
		return fmt.Errorf("session: unknown store %q", m.cfg.Store)
	}
	return nil
}

// middleware returns the HandlerFunc that loads/creates the session per request.
func (m *Module) middleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		sess := m.loadOrCreate(c.Request.Context(), c.Request)
		c.Set(contextKey, sess)
		c.Next()
	}
}

// loadOrCreate reads the signed cookie, verifies it, and loads the session from
// the store; if absent or invalid it returns a fresh empty session.
func (m *Module) loadOrCreate(ctx context.Context, r *http.Request) *session {
	if cookie, err := r.Cookie(m.cookieName()); err == nil {
		if id, err := verifyCookie(m.cfg.Secret, cookie.Value); err == nil {
			values, _, ok, err := m.store.Load(ctx, id)
			if err != nil {
				slog.Warn("session load failed", "err", err)
			} else if ok {
				return &session{id: id, values: values, mod: m}
			}
		}
	}
	return &session{values: make(map[string]any), mod: m}
}

// Session returns the active session for the request, panicking if the
// middleware did not run (i.e. the route was registered without the session
// module, or the handler is called outside a request).
func (m *Module) Session(c *inertia.Context) Session {
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

// Shutdown is a no-op; stores own no resources that need releasing at shutdown
// (db connections are owned by the db module).
func (m *Module) Shutdown(context.Context) error { return nil }

// cookieName returns the configured cookie name or the default.
func (m *Module) cookieName() string {
	return defaultStr(m.cfg.CookieName, "session")
}

// ttl returns the configured TTL or the default.
func (m *Module) ttl() time.Duration {
	if m.cfg.TTL > 0 {
		return m.cfg.TTL
	}
	return 24 * time.Hour
}

// httpOnlyFlag returns the effective HttpOnly setting. The zero value means
// true (secure default); users opt out by… there is no opt-out field, so it is
// always true. Kept as a method for clarity and future extension.
func (m *Module) httpOnlyFlag() bool {
	// HttpOnly defaults to true for security; the config field is reserved for
	// a future explicit override but currently always-on.
	return true
}

// sameSite maps the config string to http.SameSite.
func (m *Module) sameSite() http.SameSite {
	switch m.cfg.SameSite {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

// setCookie writes the signed session cookie on the response.
func (m *Module) setCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName(),
		Value:    signCookie(m.cfg.Secret, id),
		Path:     defaultStr(m.cfg.Path, "/"),
		Domain:   m.cfg.Domain,
		MaxAge:   int(m.ttl().Seconds()),
		Secure:   m.cfg.Secure,
		HttpOnly: m.httpOnlyFlag(),
		SameSite: m.sameSite(),
	})
}

// clearCookie expires the session cookie.
func (m *Module) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName(),
		Value:    "",
		Path:     defaultStr(m.cfg.Path, "/"),
		Domain:   m.cfg.Domain,
		MaxAge:   -1,
		Secure:   m.cfg.Secure,
		HttpOnly: true,
		SameSite: m.sameSite(),
	})
}

func defaultStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
