// Package admin is the authenticated admin controller area: a login/logout flow
// backed by a users table and the session service, a dashboard, per-resource
// permissions, and a menu registry filtered to what the caller may reach.
//
// Routes come in through Resource, the registrar: one call registers the route,
// attaches the permission guard, records the permission in an enumerable
// catalogue, and — for Menu — adds a sidebar entry gated by the same key. Login
// runs no middleware at all; logout and the dashboard require a session but no
// permission, so a user whose group grants nothing can still sign in and out.
//
// It needs its own *Config (mount, auth key, users table), so serve.go wires it
// explicitly rather than through the generated MountAll. The middlewares are
// attached per route, so neither ever filters by path.
package admin

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

// Defaults applied by the accessors below when [admin] is absent or a field is
// left empty.
const (
	defaultMount      = "/admin"
	defaultLoginLeaf  = "/login"
	defaultAuthKey    = "admin_user_id"
	defaultUsersTable = "users"
)

// tableNameRe restricts the users table name to a safe identifier, since it is
// interpolated directly into SQL.
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

// Admin is the admin controller. It embeds *app.Services and holds its resolved
// config, the menu registry, and the permission catalogue; a single instance is
// built at startup and shared (read-only) across requests.
type Admin struct {
	*app.Services
	cfg  *Config
	menu []menuEntry
	// perms maps a permission key to the routes it guards, filled during
	// startup wiring by the registrar and read-only afterwards.
	perms map[string][]string
}

// New constructs the admin controller. cfg may be nil; accessors apply defaults.
func New(svc *app.Services, cfg *Config) *Admin {
	return &Admin{Services: svc, cfg: cfg}
}

// Validate requires a present [admin] config section and a safe users-table
// name (interpolated into SQL). Accessors stay nil-safe so tests may skip it.
func (a *Admin) Validate() error {
	if a.cfg == nil {
		return errors.New("admin: enabled but [admin] config section missing")
	}
	if table := a.usersTable(); !tableNameRe.MatchString(table) {
		return fmt.Errorf("admin: illegal users table name %q", table)
	}
	return nil
}

// Mount registers the admin area's own routes: the public login pair, the two
// authentication-only exemptions (logout and the dashboard), the user and group
// resources through the registrar, and the account page — the third exemption,
// which must not be permission-gated. See mountAccount for why.
func (a *Admin) Mount(eng *inertia.Engine) {
	auth := a.AuthMiddleware()
	eng.GET(a.LoginPath(), a.LoginForm)    // public
	eng.POST(a.LoginPath(), a.LoginSubmit) // public
	eng.POST(a.mount()+"/logout", auth, a.Logout)
	eng.GET(a.mount(), auth, a.Dashboard)

	a.mountUsers(eng)
	a.mountGroups(eng)
	a.mountAccount(eng)
}

// Prefix returns the resolved admin mount prefix (used by generated resources).
func (a *Admin) Prefix() string { return a.mount() }

// LoginPath returns the resolved public login route.
func (a *Admin) LoginPath() string {
	if a.cfg == nil {
		return a.mount() + defaultLoginLeaf
	}
	return cmp.Or(a.cfg.LoginPath, a.mount()+defaultLoginLeaf)
}

func (a *Admin) mount() string {
	if a.cfg == nil {
		return defaultMount
	}
	return cmp.Or(a.cfg.Mount, defaultMount)
}

func (a *Admin) authKey() string {
	if a.cfg == nil {
		return defaultAuthKey
	}
	return cmp.Or(a.cfg.AuthKey, defaultAuthKey)
}

func (a *Admin) usersTable() string {
	if a.cfg == nil {
		return defaultUsersTable
	}
	return cmp.Or(a.cfg.UsersTable, defaultUsersTable)
}
