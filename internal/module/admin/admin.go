// Package admin is a feature module providing an authenticated admin framework.
//
// It is an app.Module that owns the admin shell: an auth middleware guarding the
// configured mount, a login/logout flow backed by a users table (via the db
// module) and the session module, a dashboard, and a menu registry that
// generated admin resources register into. Generated resources (via
// `goapp gen admin <name>`) mount their routes under the admin prefix and are
// automatically protected and linked in the menu.
//
// Ordering contract (§4.2): admin must be registered AFTER session (its auth
// check reads the loaded session) and BEFORE any admin resource module (so the
// resource's Register can call AddMenuItem and Mount()).
package admin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/module/session"
	"github.com/millken/inertia"
)

const defaultAuthKey = "admin_user_id"

// tableNameRe restricts the users table name to a safe identifier so it can be
// interpolated into SQL without quoting concerns.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// SessionProvider is the dependency on the session module. Satisfied by
// *session.Module.
type SessionProvider interface {
	Session(c *inertia.Context) session.Session
}

// DBProvider is the dependency on the db module. Satisfied by *db.Module.
type DBProvider interface {
	DB() *sqldb.DB
}

// Config configures the admin module. Pointer in New so Boot can enforce the
// enable-consistency rule (§4.4).
type Config struct {
	// Mount is the admin URL prefix (default "/admin").
	Mount string `yaml:"mount"`
	// LoginPath is the public login route (default Mount+"/login").
	LoginPath string `yaml:"login_path"`
	// AuthKey is the session key whose truthy presence means "logged in"
	// (default "admin_user_id"); its value is the authenticated user id.
	AuthKey string `yaml:"auth_key"`
	// UsersTable is the table login authenticates against (default "users").
	UsersTable string `yaml:"users_table"`
}

// Module is the admin framework module.
type Module struct {
	cfg      *Config
	sessProv SessionProvider
	dbProv   DBProvider
	menu     []MenuItem
}

// New constructs the admin module. Both providers are required: session for
// auth state, db for user lookup.
func New(cfg *Config, sessProv SessionProvider, dbProv DBProvider) *Module {
	return &Module{cfg: cfg, sessProv: sessProv, dbProv: dbProv}
}

// Register installs the auth middleware and the framework's own routes (login,
// logout, dashboard). The middleware guards the mount, so every subsequently
// registered admin route is protected.
func (m *Module) Register(a *app.App) error {
	a.Engine.Use(m.authMiddleware())
	mount := m.mount()
	a.Engine.GET(m.loginPath(), m.loginForm)
	a.Engine.POST(m.loginPath(), m.loginSubmit)
	a.Engine.POST(mount+"/logout", m.logout)
	a.Engine.GET(mount, m.dashboard)
	return nil
}

// Boot validates config and dependencies. No IO: user lookup happens at request
// time, by which point db has Booted (admin is Use'd after db/session).
func (m *Module) Boot(_ context.Context) error {
	if m.cfg == nil {
		return errors.New("admin: module enabled but [admin] config section missing")
	}
	if m.sessProv == nil {
		return errors.New("admin: a session.Provider is required")
	}
	if m.dbProv == nil {
		return errors.New("admin: a db.Provider is required")
	}
	if !tableNameRe.MatchString(m.usersTable()) {
		return fmt.Errorf("admin: illegal users table name %q", m.usersTable())
	}
	return nil
}

// Shutdown is a no-op (no owned resources; db connections belong to the db module).
func (m *Module) Shutdown(context.Context) error { return nil }

// Mount returns the resolved admin mount prefix. Generated admin resource
// modules call it to build their route paths.
func (m *Module) Mount() string { return m.mount() }

// resolved config accessors (defaults applied here, not in the struct). They are
// nil-safe: Register (and the middleware closure it builds) runs before Boot, so
// they may be called while cfg is still nil — a missing [admin] section must
// surface as Boot's clear error, never a nil-dereference panic during Register.
func (m *Module) mount() string {
	if m.cfg == nil {
		return "/admin"
	}
	return cmp.Or(m.cfg.Mount, "/admin")
}
func (m *Module) loginPath() string {
	if m.cfg == nil {
		return m.mount() + "/login"
	}
	return cmp.Or(m.cfg.LoginPath, m.mount()+"/login")
}
func (m *Module) authKey() string {
	if m.cfg == nil {
		return defaultAuthKey
	}
	return cmp.Or(m.cfg.AuthKey, defaultAuthKey)
}
func (m *Module) usersTable() string {
	if m.cfg == nil {
		return "users"
	}
	return cmp.Or(m.cfg.UsersTable, "users")
}
