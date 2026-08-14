// Package admin is the authenticated admin controller area: a login/logout flow
// backed by the `admins` table and the session service, a dashboard,
// per-resource permissions, and a menu registry filtered to what the caller may
// reach.
//
// The accounts live in `admins`, not `users`, and the same holds for
// `admin_groups` and `admin_login_attempts`: these rows are operators of this
// area, and naming them `users` would take the one table name an application is
// most likely to want for its own end users. The Go identifiers here still read
// "user" — a userRow, /admin/user — because inside package admin there is
// nothing else a user could be.
//
// Routes come in through Resource, the registrar: one call registers the route,
// attaches the permission guard, records the permission in an enumerable
// catalogue, and — for Menu — adds a sidebar entry gated by the same key.
//
// Three routes deliberately do not: logout, the dashboard, and the account
// password page all take AuthMiddleware instead, requiring a session but no
// permission. A user whose group grants nothing can therefore still sign in,
// see where they are, change their own password, and sign out. Login itself
// runs no middleware at all. That list of three is the complete answer to
// "which routes skip the registrar" — see mountAccount for why the last one
// has to be on it.
//
// It needs its own *Config (mount, auth key, admins table), so serve.go wires it
// explicitly rather than through the generated MountAll. The middlewares are
// attached per route, so neither ever filters by path.
package admin

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/inertia"
)

// Defaults applied by the accessors below when [admin] is absent or a field is
// left empty.
const (
	defaultMount       = "/admin"
	defaultLoginLeaf   = "/login"
	defaultAuthKey     = "admin_user_id"
	defaultAdminsTable = "admins"
)

// tableNameRe restricts the admins table name to a safe identifier, since it is
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
	// Table is the table login authenticates against (default "admins"). It is
	// interpolated into SQL, so Validate holds it to ^[A-Za-z_]\w*$.
	Table string `yaml:"table"`
	// TrustedProxies are CIDR blocks whose X-Forwarded-For is believed when
	// resolving the client address for the login throttle. Empty — the default —
	// means the header is never read, because anyone can send it.
	TrustedProxies []string `yaml:"trusted_proxies"`
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
	// trusted is cfg.TrustedProxies parsed once by Validate.
	trusted []netip.Prefix
}

// New constructs the admin controller. cfg may be nil; accessors apply defaults.
func New(svc *app.Services, cfg *Config) *Admin {
	return &Admin{Services: svc, cfg: cfg}
}

// Validate requires a present [admin] config section and a safe admins-table
// name (interpolated into SQL). Accessors stay nil-safe so tests may skip it.
func (a *Admin) Validate() error {
	if a.cfg == nil {
		return errors.New("admin: enabled but [admin] config section missing")
	}
	if table := a.adminsTable(); !tableNameRe.MatchString(table) {
		return fmt.Errorf("admin: illegal admins table name %q", table)
	}
	// Parsed once, here, so a typo stops startup. Left as a warning it would
	// silently empty the trust list, and an empty trust list silently disables
	// the login throttle's only defence against a forged X-Forwarded-For.
	a.trusted = nil
	for _, raw := range a.cfg.TrustedProxies {
		p, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("admin: trusted_proxies %q: %w", raw, err)
		}
		a.trusted = append(a.trusted, p)
	}
	return nil
}

// Mount registers the admin area's own routes: the public login pair, the two
// authentication-only exemptions (logout and the dashboard), the user, group
// and file manager resources through the registrar, and the account page — the
// third exemption, which must not be permission-gated. See mountAccount for why.
func (a *Admin) Mount(eng *inertia.Engine) {
	auth := a.AuthMiddleware()
	eng.GET(a.LoginPath(), a.LoginForm)    // public
	eng.POST(a.LoginPath(), a.LoginSubmit) // public
	eng.POST(a.mount()+"/logout", auth, a.Logout)
	eng.GET(a.mount(), auth, a.Dashboard)

	a.mountUsers(eng)
	a.mountGroups(eng)
	a.mountAccount(eng)

	//goappctl:storage
	// Unconditional, like every other resource. Mounting reads no Storage —
	// the handlers resolve it per request — so a test may fill svc.Storage
	// after Mount, and a build carrying this block always has the routes. A
	// nil check here would mean a misconfigured process silently serving an
	// admin area with no file manager instead of failing at Start.
	a.mountFileManager(eng)
	//goappctl:end
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

func (a *Admin) adminsTable() string {
	if a.cfg == nil {
		return defaultAdminsTable
	}
	return cmp.Or(a.cfg.Table, defaultAdminsTable)
}
