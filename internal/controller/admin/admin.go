// Package admin is the authenticated admin controller area.
//
// It owns the admin shell: an auth middleware guarding the admin routes, a
// login/logout flow backed by a users table (via app.Services.DB) and the
// session service, a dashboard, and a menu registry that generated admin
// resources register into.
//
// Unlike an ordinary controller area it needs its own *Config (mount, auth key,
// users table), so serve.go wires it explicitly with admin.Mount(eng, svc,
// cfg.Admin) rather than through the generated MountAll. The auth middleware is
// attached only to the protected routes, so it never has to self-filter by path.
package admin

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

const defaultAuthKey = "admin_user_id"

// tableNameRe restricts the users table name to a safe identifier so it can be
// interpolated into SQL without quoting concerns.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// Config configures the admin area.
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

// Admin is the admin controller. It embeds *app.Services (DB/Session/Log) and
// holds its resolved config plus the menu registry. A single instance is
// constructed by Mount at startup and shared across requests (read-only after
// setup).
type Admin struct {
	*app.Services
	cfg  *Config
	menu []MenuItem
}

// New constructs the admin controller from the service container and its config.
// cfg may be nil; resolved accessors apply defaults.
func New(svc *app.Services, cfg *Config) *Admin {
	return &Admin{Services: svc, cfg: cfg}
}

// Validate enforces the enable-consistency rule: a wired admin area must have
// its [admin] config section present, and the users-table name must be a safe
// identifier (it is interpolated into SQL). Accessors remain nil-safe so tests
// may use New(svc, nil) without Validate.
func (a *Admin) Validate() error {
	if a.cfg == nil {
		return errors.New("admin: enabled but [admin] config section missing")
	}
	if !tableNameRe.MatchString(a.usersTable()) {
		return fmt.Errorf("admin: illegal users table name %q", a.usersTable())
	}
	return nil
}

// Mount wires the admin shell onto eng: public login routes plus the mount and
// logout guarded by the auth middleware. serve.go calls it after Validate.
func (a *Admin) Mount(eng *inertia.Engine) {
	auth := a.AuthMiddleware()
	eng.GET(a.LoginPath(), a.LoginForm)    // public
	eng.POST(a.LoginPath(), a.LoginSubmit) // public
	eng.POST(a.mount()+"/logout", auth, a.Logout)
	eng.GET(a.mount(), auth, a.Dashboard)
}

// Prefix returns the resolved admin mount prefix. Generated admin resources call
// it to build their route paths.
func (a *Admin) Prefix() string { return a.mount() }

// LoginPath returns the resolved public login route.
func (a *Admin) LoginPath() string {
	if a.cfg == nil {
		return a.mount() + "/login"
	}
	return cmp.Or(a.cfg.LoginPath, a.mount()+"/login")
}

func (a *Admin) mount() string {
	if a.cfg == nil {
		return "/admin"
	}
	return cmp.Or(a.cfg.Mount, "/admin")
}

func (a *Admin) authKey() string {
	if a.cfg == nil {
		return defaultAuthKey
	}
	return cmp.Or(a.cfg.AuthKey, defaultAuthKey)
}

func (a *Admin) usersTable() string {
	if a.cfg == nil {
		return "users"
	}
	return cmp.Or(a.cfg.UsersTable, "users")
}
