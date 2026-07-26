# Admin Permissions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the admin area per-resource permissions — `<resource>.access` and `<resource>.modify` — enforced by the route middleware, so a route cannot be registered without its check.

**Architecture:** A registrar returned by `adm.Resource(eng, name)` registers each route, attaches a guard already bound to the permission key the HTTP method implies, and records the key in an enumerable catalogue. Permissions live on a `user_groups` row as a JSON array of keys; the guard resolves the session's user to a group with one indexed query per request.

**Tech Stack:** Go 1.26 stdlib + `github.com/dnsoa/go/sqldb` (already a dependency); SQLite for tests via `sqldb.Open("sqlite3", ":memory:")`.

## Global Constraints

- **The permission unit is resource + verb.** GET/HEAD → `<resource>.access`; every other method → `<resource>.modify`. Never per-route.
- **`modify` implies `access`, one way only.** A group holding `post.modify` passes a `post.access` check; a group holding only `post.access` must fail a `post.modify` check.
- **The registrar attaches `guard(key)` alone, never `AuthMiddleware` as well** — stacking both would resolve the group twice per request and break the one-query-per-request property.
- **A database error is a 500, never a 403.** A storage problem must not read as an authorisation decision.
- **Fail closed:** a user with no group row gets 403.
- **Exempt routes** (authenticated, no permission): `GET`/`POST` login, `POST /admin/logout`, `GET /admin` dashboard.
- **Both middlewares filter the menu identically**, via one shared helper. Neither owns a copy.
- **Migrations own the literal `users` table.** `users_table` redirects runtime lookups only; embedded SQL cannot read configuration.
- **No management UI.** No group CRUD, no permission editor — stage 2.
- `cmd/goappctl/internal/scaffold/templates/resource/*` and `internal/controller/site/**` must not be modified.
- Verification: `go build ./...`, `go vet ./...`, `gofmt -l` clean, `go test ./... -count=1`, `pnpm -C frontend type-check`.

---

## Task 1: Permission keys, the permission set, and the catalogue

Pure logic — no database, no middleware. Everything later depends on these names.

**Files:**
- Create: `internal/controller/admin/permission.go`
- Test: `internal/controller/admin/permission_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func permKey(resource, method string) string`
  - `type permSet map[string]bool` with `func (p permSet) Allows(key string) bool`
  - `type Permission struct { Key string; Routes []string }`
  - `func (a *Admin) recordPermission(key, method, path string)` — appends to the catalogue
  - `func (a *Admin) Permissions() []Permission` — sorted by key, routes deduplicated

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/permission_test.go`:

```go
package admin

import (
	"net/http"
	"testing"
)

func TestPermKey(t *testing.T) {
	for _, c := range []struct{ method, want string }{
		{http.MethodGet, "post.access"},
		{http.MethodHead, "post.access"},
		{http.MethodPost, "post.modify"},
		{http.MethodPut, "post.modify"},
		{http.MethodPatch, "post.modify"},
		{http.MethodDelete, "post.modify"},
	} {
		if got := permKey("post", c.method); got != c.want {
			t.Errorf("permKey(post, %s) = %q, want %q", c.method, got, c.want)
		}
	}
}

// The implication is one-way. A test that only checked modify→access would pass
// for an implementation that allowed everything.
func TestPermSetAllows(t *testing.T) {
	modifyOnly := permSet{"post.modify": true}
	if !modifyOnly.Allows("post.modify") {
		t.Error("modify should allow modify")
	}
	if !modifyOnly.Allows("post.access") {
		t.Error("modify should imply access")
	}

	accessOnly := permSet{"post.access": true}
	if !accessOnly.Allows("post.access") {
		t.Error("access should allow access")
	}
	if accessOnly.Allows("post.modify") {
		t.Error("access must NOT imply modify — the implication is one-way")
	}

	empty := permSet{}
	if empty.Allows("post.access") || empty.Allows("post.modify") {
		t.Error("an empty set allows nothing")
	}

	// A different resource's key must not leak across.
	if modifyOnly.Allows("user.access") {
		t.Error("post.modify must not allow user.access")
	}
}

func TestPermissionsCatalogue(t *testing.T) {
	a := New(nil, nil)
	a.recordPermission("post.access", http.MethodGet, "/admin/post")
	a.recordPermission("post.access", http.MethodGet, "/admin/post/:id/edit")
	a.recordPermission("post.modify", http.MethodPost, "/admin/post")
	a.recordPermission("user.access", http.MethodGet, "/admin/user")
	// The same route twice must not duplicate.
	a.recordPermission("post.access", http.MethodGet, "/admin/post")

	got := a.Permissions()
	if len(got) != 3 {
		t.Fatalf("want 3 keys, got %d: %+v", len(got), got)
	}
	// Sorted by key, so a UI built on this is stable.
	want := []string{"post.access", "post.modify", "user.access"}
	for i, k := range want {
		if got[i].Key != k {
			t.Errorf("key %d = %q, want %q", i, got[i].Key, k)
		}
	}
	if n := len(got[0].Routes); n != 2 {
		t.Errorf("post.access should list 2 routes, got %d: %v", n, got[0].Routes)
	}
	if got[0].Routes[0] != "GET /admin/post" {
		t.Errorf("route format = %q, want %q", got[0].Routes[0], "GET /admin/post")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/admin/ -run 'PermKey|PermSet|PermissionsCatalogue' -count=1`

Expected: FAIL to compile — `undefined: permKey`, `undefined: permSet`, `undefined: recordPermission`, `undefined: Permissions`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/admin/permission.go`:

```go
package admin

import (
	"net/http"
	"slices"
	"strings"
)

// Permission verbs. A route's verb comes from its HTTP method, so nothing has to
// name it: reading is access, changing anything is modify.
const (
	verbAccess = ".access"
	verbModify = ".modify"
)

// permKey is the permission a method implies for a resource. Only GET and HEAD
// read; anything else is treated as a change, so a method nobody anticipated
// errs toward requiring more permission rather than less.
func permKey(resource, method string) string {
	switch method {
	case http.MethodGet, http.MethodHead:
		return resource + verbAccess
	default:
		return resource + verbModify
	}
}

// permSet is one group's permissions, keyed for O(1) lookup.
type permSet map[string]bool

// Allows reports whether the set permits key. modify implies access — a group
// that may change a resource may obviously read it — but not the reverse.
func (p permSet) Allows(key string) bool {
	if p[key] {
		return true
	}
	if resource, isAccess := strings.CutSuffix(key, verbAccess); isAccess {
		return p[resource+verbModify]
	}
	return false
}

// Permission is one catalogue entry: a key and the routes it guards. The routes
// are recorded so a management screen can explain a permission rather than
// showing a bare string.
type Permission struct {
	Key    string   `json:"key"`
	Routes []string `json:"routes"`
}

// recordPermission adds a route to the catalogue under key. Called during
// startup wiring only, so no locking is needed — same contract as AddMenuItem.
func (a *Admin) recordPermission(key, method, path string) {
	route := method + " " + path
	if a.perms == nil {
		a.perms = map[string][]string{}
	}
	if slices.Contains(a.perms[key], route) {
		return
	}
	a.perms[key] = append(a.perms[key], route)
}

// Permissions returns every registered key with the routes it guards, sorted by
// key so a UI built on it is stable.
func (a *Admin) Permissions() []Permission {
	out := make([]Permission, 0, len(a.perms))
	for key, routes := range a.perms {
		out = append(out, Permission{Key: key, Routes: slices.Clone(routes)})
	}
	slices.SortFunc(out, func(x, y Permission) int { return strings.Compare(x.Key, y.Key) })
	return out
}
```

- [ ] **Step 4: Add the catalogue field to `Admin`**

In `internal/controller/admin/admin.go`, the `Admin` struct currently reads:

```go
type Admin struct {
	*app.Services
	cfg  *Config
	menu []MenuItem
}
```

Add the catalogue field (leave `menu` alone — Task 3 changes it):

```go
type Admin struct {
	*app.Services
	cfg  *Config
	menu []MenuItem
	// perms maps a permission key to the routes it guards, filled during
	// startup wiring by the registrar and read-only afterwards.
	perms map[string][]string
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/controller/admin/ -count=1`

Expected: PASS, including the package's pre-existing tests.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/admin/permission.go internal/controller/admin/permission_test.go internal/controller/admin/admin.go
git commit -m "feat(admin): permission keys, sets, and an enumerable catalogue

The verb comes from the HTTP method, so no route has to name its permission, and
an unanticipated method errs toward requiring modify rather than access. modify
implies access one way only — a group that may change a resource may read it, but
not the reverse. The catalogue records which routes each key guards so a later
screen can explain a permission instead of showing a bare string."
```

---

## Task 2: The `user_groups` schema and the group lookup

The migration and the query that reads it, together — the query has nothing to read without the schema.

**Files:**
- Create: `internal/service/db/migrations/003_user_groups.up.sql`
- Create: `internal/service/db/migrations/003_user_groups.down.sql`
- Create: `internal/controller/admin/group.go`
- Test: `internal/controller/admin/group_test.go`

**Interfaces:**
- Consumes: `permSet` from Task 1.
- Produces:
  - `type group struct { Superuser bool; Permissions permSet }`
  - `var errNoGroup = errors.New("admin: user has no group")`
  - `func findGroup(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*group, error)`

- [ ] **Step 1: Write the migration files**

Create `internal/service/db/migrations/003_user_groups.up.sql`:

```sql
-- 003_user_groups.up.sql
-- Admin permission groups. A group holds a flat JSON array of permission keys
-- ("post.access", "post.modify"); superuser bypasses the check entirely.
--
-- SQLite flavor (the template's default driver). For other dialects adjust the
-- id/auto-increment line:
--   PostgreSQL: id BIGSERIAL PRIMARY KEY
--   MySQL:      id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY
-- created_at is UnixNano (BIGINT), matching the users and sessions convention;
-- hence the strftime multiplication in the seed below.
--
-- This migration writes the literal `users` table. The [admin] users_table
-- setting redirects runtime lookups only — embedded SQL cannot read config — so
-- pointing it elsewhere makes that table's schema the operator's responsibility.
CREATE TABLE IF NOT EXISTS user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    superuser   INTEGER NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
);

ALTER TABLE users ADD COLUMN group_id INTEGER REFERENCES user_groups(id);

-- Seeded so the first `admin create-user` has somewhere to put a user that can
-- actually reach the admin area. A plain INSERT is safe: the migrator records a
-- version and wraps each file in a transaction, so this runs exactly once.
INSERT INTO user_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]',
        CAST(strftime('%s', 'now') AS INTEGER) * 1000000000);
```

Create `internal/service/db/migrations/003_user_groups.down.sql`:

```sql
-- 003_user_groups.down.sql
-- The column goes before the table it references. DROP COLUMN needs SQLite 3.35+
-- (mattn/go-sqlite3 bundles 3.53); MySQL and PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN group_id;
DROP TABLE IF EXISTS user_groups;
```

- [ ] **Step 2: Write the failing test**

Create `internal/controller/admin/group_test.go`:

```go
package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/dnsoa/go/sqldb"
	_ "github.com/mattn/go-sqlite3"
)

// groupFixture builds the two tables this package reads and returns an open
// handle. It creates them directly rather than running the migrator, so a
// failure here points at the query rather than at migration wiring.
func groupFixture(t *testing.T) *sqldb.DB {
	t.Helper()
	d, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	for _, q := range []string{
		`CREATE TABLE user_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			superuser INTEGER NOT NULL DEFAULT 0,
			permissions TEXT NOT NULL DEFAULT '[]',
			created_at BIGINT NOT NULL)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			group_id INTEGER REFERENCES user_groups(id))`,
		`INSERT INTO user_groups (id, name, superuser, permissions, created_at)
		 VALUES (1, 'Administrators', 1, '[]', 0)`,
		`INSERT INTO user_groups (id, name, superuser, permissions, created_at)
		 VALUES (2, 'Editors', 0, '["post.modify","user.access"]', 0)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (1, 'root', 'x', 0, 1)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (2, 'editor', 'x', 0, 2)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (3, 'orphan', 'x', 0, NULL)`,
	} {
		if _, err := d.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
	return d
}

func TestFindGroup(t *testing.T) {
	d := groupFixture(t)
	ctx := context.Background()

	su, err := findGroup(ctx, d, "users", 1)
	if err != nil {
		t.Fatalf("superuser: %v", err)
	}
	if !su.Superuser {
		t.Error("user 1 should be a superuser")
	}

	ed, err := findGroup(ctx, d, "users", 2)
	if err != nil {
		t.Fatalf("editor: %v", err)
	}
	if ed.Superuser {
		t.Error("user 2 should not be a superuser")
	}
	if !ed.Permissions.Allows("post.modify") {
		t.Error("editor should hold post.modify")
	}
	if !ed.Permissions.Allows("post.access") {
		t.Error("post.modify should imply post.access")
	}
	if ed.Permissions.Allows("user.modify") {
		t.Error("editor holds only user.access, so user.modify must fail")
	}
}

// Fail closed: no group row is not the same as an empty permission set, but both
// deny — and the caller must be able to tell this apart from a database error.
func TestFindGroup_NoGroupIsErrNoGroup(t *testing.T) {
	d := groupFixture(t)

	if _, err := findGroup(context.Background(), d, "users", 3); !errors.Is(err, errNoGroup) {
		t.Errorf("orphaned user: got %v, want errNoGroup", err)
	}
	if _, err := findGroup(context.Background(), d, "users", 999); !errors.Is(err, errNoGroup) {
		t.Errorf("unknown user: got %v, want errNoGroup", err)
	}
}

// A malformed permissions column is a storage problem, not a denial — the caller
// turns errNoGroup into 403 and anything else into 500, so these must differ.
func TestFindGroup_BadJSONIsNotErrNoGroup(t *testing.T) {
	d := groupFixture(t)
	if _, err := d.ExecContext(context.Background(),
		`UPDATE user_groups SET permissions = 'not json' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	_, err := findGroup(context.Background(), d, "users", 2)
	if err == nil {
		t.Fatal("want an error for malformed JSON")
	}
	if errors.Is(err, errNoGroup) {
		t.Error("malformed JSON must not be reported as a missing group")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/controller/admin/ -run FindGroup -count=1`

Expected: FAIL to compile — `undefined: findGroup`, `undefined: errNoGroup`.

- [ ] **Step 4: Write the implementation**

Create `internal/controller/admin/group.go`:

```go
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
)

// errNoGroup means the user exists but has no group — or no longer does, if a
// group was deleted from under them. Distinct from a database failure so the
// caller can answer 403 for one and 500 for the other.
var errNoGroup = errors.New("admin: user has no group")

// group is the authorisation state for one signed-in user.
type group struct {
	Superuser   bool
	Permissions permSet
}

// findGroup loads the group of the user with userID. usersTable is interpolated
// (it is configurable) and has already been validated by Admin.Validate against
// ^[A-Za-z_]\w*$; the id itself is parameterised.
func findGroup(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*group, error) {
	q := fmt.Sprintf(`SELECT g.superuser, g.permissions
		FROM %s u JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, usersTable)

	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, errNoGroup
		}
		return nil, fmt.Errorf("admin: find group: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("admin: group %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return &group{Superuser: superuser != 0, Permissions: set}, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/controller/admin/ -count=1`

Expected: PASS.

- [ ] **Step 6: Verify the migration applies once, seeds, and rolls back**

Create `internal/service/db/migrations_003_test.go` — a real test, not a scratch
file, because §6.1's DROP COLUMN claim and §6.2's applied-once claim are otherwise
unguarded. It lives in the `db` package so it can reach the unexported
`migrationFS`.

```go
package db

import (
	"context"
	"io/fs"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

// newMigratedService opens a shared in-memory database and runs every migration.
// MaxOpenConns=1 so migrations and queries hit the same :memory: connection —
// otherwise each gets a fresh, empty database. Same reason login_test.go does it.
func newMigratedService(t *testing.T, dsn string) *Service {
	t.Helper()
	s := New(&Config{
		Driver:       "sqlite3",
		DSN:          dsn,
		MaxOpenConns: 1,
		Migrations:   &Migrations{},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func TestMigration003_SeedsAdministratorsAndAddsGroupID(t *testing.T) {
	s := newMigratedService(t, ":memory:")
	ctx := context.Background()

	var name string
	var superuser int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT name, superuser FROM user_groups WHERE name = 'Administrators'`).
		Scan(&name, &superuser); err != nil {
		t.Fatalf("Administrators not seeded: %v", err)
	}
	if superuser != 1 {
		t.Errorf("Administrators.superuser = %d, want 1", superuser)
	}

	// The column must exist even with no rows, so select it rather than a row.
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err != nil {
		t.Errorf("users.group_id missing: %v", err)
	}
}

// ALTER TABLE ADD COLUMN is not idempotent — a second run fails with "duplicate
// column name". This pins the property the design actually relies on: the
// migrator records a version and applies each file once. A regression in that
// tracking surfaces here rather than in production.
func TestMigration003_AppliedOnlyOnce(t *testing.T) {
	const dsn = "file:mig003?mode=memory&cache=shared"
	_ = newMigratedService(t, dsn)

	// A second Service over the same database re-runs MigrateUp.
	s2 := New(&Config{
		Driver:       "sqlite3",
		DSN:          dsn,
		MaxOpenConns: 1,
		Migrations:   &Migrations{},
	})
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("second Start re-applied a migration: %v", err)
	}
	_ = s2.Stop(context.Background())
}

// §6.1's claim: DROP COLUMN works on a column declared with REFERENCES. SQLite
// only gained DROP COLUMN in 3.35 and refuses indexed, unique or primary-key
// columns, so this is the part worth testing rather than assuming.
func TestMigration003_RollsBackCleanly(t *testing.T) {
	s := newMigratedService(t, "file:mig003down?mode=memory&cache=shared")
	ctx := context.Background()

	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub FS: %v", err)
	}
	if err := s.DB().MigrateTo(ctx, sub, "002"); err != nil {
		t.Fatalf("migrate down to 002: %v", err)
	}

	// Both the table and the column must be gone.
	if _, err := s.DB().ExecContext(ctx, `SELECT 1 FROM user_groups WHERE 1 = 0`); err == nil {
		t.Error("user_groups survived the down migration")
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err == nil {
		t.Error("users.group_id survived the down migration")
	}

	// And up again, so the pair is reversible rather than one-way.
	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT group_id FROM users WHERE 1 = 0`); err != nil {
		t.Errorf("users.group_id missing after re-apply: %v", err)
	}
}
```

Run: `go test ./internal/service/db/ -count=1`

Expected: PASS. If `MigrateTo`'s version argument wants a different spelling than
`"002"`, read `migration.go` in `github.com/dnsoa/go/sqldb` for the format it
records and use that — report which you used. If `MigrateTo` is not reachable
through `*sqldb.DB`, report that and leave `TestMigration003_RollsBackCleanly`
out rather than inventing a path to it; the other two tests still stand.

- [ ] **Step 7: Commit**

```bash
git add internal/service/db/migrations/003_user_groups.up.sql \
  internal/service/db/migrations/003_user_groups.down.sql \
  internal/service/db/migrations_003_test.go \
  internal/controller/admin/group.go internal/controller/admin/group_test.go
git commit -m "feat(admin): user_groups schema and the group lookup

A group holds a flat JSON array of permission keys plus a superuser flag — read
whole on every request and never queried by permission, so a join table would add
a join for no query it enables.

errNoGroup is deliberately distinct from a database failure: the caller answers
403 for a user with no group and 500 for a storage problem, and conflating them
would make an outage look like a permissions decision. Malformed JSON in the
column is likewise a 500, not a denial."
```

---

## Task 3: Shared resolution, and menu filtering in one place

`AuthMiddleware` gains the group lookup so the menu it injects is filtered. This is what keeps the dashboard's sidebar consistent with a guarded page's.

**Files:**
- Modify: `internal/controller/admin/menu.go`
- Modify: `internal/controller/admin/auth.go`
- Modify: `internal/controller/admin/admin.go` (the `menu` field's type)
- Test: `internal/controller/admin/menu_test.go` (create)

**Interfaces:**
- Consumes: `group`, `permSet`, `findGroup`, `errNoGroup`.
- Produces:
  - `type menuEntry struct { item MenuItem; resource string }`
  - `func (a *Admin) addResourceMenuItem(item MenuItem, resource string)`
  - `func (a *Admin) menuItems(g *group) []MenuItem` — filtered
  - `func (a *Admin) resolve(c *inertia.Context) (*group, bool)` — false means it has already responded

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/menu_test.go`:

```go
package admin

import "testing"

// The convention is "empty resource means always show". Asserted on menuEntry
// directly: AddMenuItem is only one way to produce an empty resource, so testing
// it instead would leave the convention itself unpinned.
func TestMenuItems_EmptyResourceAlwaysShows(t *testing.T) {
	a := New(nil, nil)
	a.menu = []menuEntry{{item: MenuItem{Title: "Docs", Path: "/admin/docs"}}}

	for _, g := range []*group{
		{Permissions: permSet{}},
		{Superuser: true},
	} {
		got := a.menuItems(g)
		if len(got) != 1 || got[0].Title != "Docs" {
			t.Errorf("group %+v: an entry with no resource must always show, got %+v", g, got)
		}
	}
}

func TestMenuItems_FiltersByAccess(t *testing.T) {
	a := New(nil, nil)
	a.menu = []menuEntry{
		{item: MenuItem{Title: "Post", Path: "/admin/post"}, resource: "post"},
		{item: MenuItem{Title: "User", Path: "/admin/user"}, resource: "user"},
	}

	only := a.menuItems(&group{Permissions: permSet{"post.access": true}})
	if len(only) != 1 || only[0].Title != "Post" {
		t.Errorf("want only Post, got %+v", only)
	}

	// modify implies access, so an entry is reachable through either key.
	viaModify := a.menuItems(&group{Permissions: permSet{"user.modify": true}})
	if len(viaModify) != 1 || viaModify[0].Title != "User" {
		t.Errorf("user.modify should surface the User entry, got %+v", viaModify)
	}

	all := a.menuItems(&group{Superuser: true})
	if len(all) != 2 {
		t.Errorf("a superuser sees everything, got %+v", all)
	}

	none := a.menuItems(&group{Permissions: permSet{}})
	if len(none) != 0 {
		t.Errorf("no permissions means no resource entries, got %+v", none)
	}
}

func TestAddMenuItem_ProducesAnEmptyResource(t *testing.T) {
	a := New(nil, nil)
	a.AddMenuItem(MenuItem{Title: "Hand-written", Path: "/admin/x"})
	if len(a.menu) != 1 || a.menu[0].resource != "" {
		t.Errorf("AddMenuItem should leave resource empty, got %+v", a.menu)
	}
}

func TestAddResourceMenuItem_RecordsTheResource(t *testing.T) {
	a := New(nil, nil)
	a.addResourceMenuItem(MenuItem{Title: "Post", Path: "/admin/post"}, "post")
	if len(a.menu) != 1 || a.menu[0].resource != "post" {
		t.Errorf("want resource post, got %+v", a.menu)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/admin/ -run Menu -count=1`

Expected: FAIL to compile — `undefined: menuEntry`, `undefined: addResourceMenuItem`, and `menuItems` taking no arguments.

- [ ] **Step 3: Change the menu store**

In `internal/controller/admin/admin.go`, change the `menu` field's type:

```go
	menu []menuEntry
```

Replace the body of `internal/controller/admin/menu.go` below the `MenuItem`
type with:

```go
// menuEntry pairs a sidebar item with the resource whose access key gates it. An
// empty resource means "always show" — that is what AddMenuItem produces, for
// entries outside the permission model.
type menuEntry struct {
	item     MenuItem
	resource string
}

// AddMenuItem registers a navigation entry that no permission gates. Call only
// during startup wiring (before Serve); the menu is read at request time, so no
// locking is needed.
func (a *Admin) AddMenuItem(item MenuItem) {
	a.menu = append(a.menu, menuEntry{item: item})
}

// addResourceMenuItem registers an entry gated by resource's access key. Used by
// the registrar; resources go through Registrar.Menu rather than calling this.
func (a *Admin) addResourceMenuItem(item MenuItem, resource string) {
	a.menu = append(a.menu, menuEntry{item: item, resource: resource})
}

// menuItems returns the sorted menu with entries g may not access removed. A
// sidebar full of links that all 403 is worse than a short sidebar.
func (a *Admin) menuItems(g *group) []MenuItem {
	// make+append rather than slices.Clone: an empty menu must reach the
	// frontend as [] rather than null.
	out := make([]MenuItem, 0, len(a.menu))
	for _, e := range a.menu {
		if e.resource != "" && !g.Superuser && !g.Permissions.Allows(e.resource+verbAccess) {
			continue
		}
		out = append(out, e.item)
	}
	slices.SortStableFunc(out, func(x, y MenuItem) int {
		return cmp.Or(cmp.Compare(x.Order, y.Order), cmp.Compare(x.Title, y.Title))
	})
	return out
}
```

- [ ] **Step 4: Extract `resolve` and rewrite `AuthMiddleware`**

Replace the body of `internal/controller/admin/auth.go` with:

```go
package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/millken/inertia"
)

// resolve authenticates the request, loads the caller's group, and injects the
// props every admin page expects — including the menu, filtered to what the
// caller may reach. It reports false when it has already written a response
// (redirect to login, 403, or 500), in which case the caller must stop.
//
// Both middlewares go through here, so the dashboard (exempt from
// authorisation) still gets the same filtered menu a guarded page gets. It is
// also why the group lookup is not confined to guard.
func (a *Admin) resolve(c *inertia.Context) (*group, bool) {
	sess := a.Session.Session(c)
	v, ok := sess.Get(a.authKey())
	if !ok || v == nil || v == "" {
		// Not authenticated: redirect to login. Under PJAX this becomes a
		// {redirect} payload rather than a 302 — see inertia's Context.Redirect.
		if err := c.Redirect(a.LoginPath()); err != nil {
			slog.Error("admin auth: redirect to login", "err", err)
		}
		c.Abort()
		return nil, false
	}

	id, ok := v.(int64)
	if !ok {
		// The session holds whatever LoginSubmit stored; a different type means
		// a stale or tampered session rather than a permission decision.
		slog.Error("admin auth: session user id is not an int64", "value", v)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	g, err := findGroup(c.Request.Context(), a.DB, a.usersTable(), id)
	switch {
	case errors.Is(err, errNoGroup):
		// Fail closed. A user with no group has no permissions, and that is a
		// denial rather than an outage.
		c.AbortWithStatus(http.StatusForbidden)
		return nil, false
	case err != nil:
		// A storage problem must not read as an authorisation decision.
		slog.Error("admin auth: load group", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	c.Set("adminMenu", a.menuItems(g))
	c.Set("adminUser", v)
	c.Set("adminMount", a.mount())
	c.Set("loginPath", a.LoginPath())
	return g, true
}

// AuthMiddleware enforces login on the routes it guards, without requiring any
// permission. It is what the exempt routes use: logout, and the dashboard —
// which must stay reachable, or a user with no permissions logs in, sees only
// 403, and cannot self-diagnose.
func (a *Admin) AuthMiddleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		if _, ok := a.resolve(c); !ok {
			return
		}
		c.Next()
	}
}
```

**Read the existing `auth.go` first** — it currently reads `a.authKey()` and
`a.mount()` through local variables captured outside the returned closure. Those
accessors are cheap and `resolve` needs them per request, so calling them inline
as above is correct; do not reintroduce the captures.

- [ ] **Step 5: Give the existing test harness a group, or every login test 403s**

`loginStack` in `internal/controller/admin/login_test.go` seeds its user with

```go
	`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
```

which leaves `group_id` null. From this step onward `resolve` fails closed on a
user with no group, so **every existing login test would start returning 403** —
including ones that assert a 302 or that a handler ran. This is not a flaw in
those tests; it is the new invariant reaching them.

Change that seed to place the user in the group the migration seeds:

```go
	if _, err := dbSvc.DB().ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, group_id)
		 VALUES (?, ?, ?, (SELECT id FROM user_groups WHERE name = 'Administrators'))`,
		"alice", hash, time.Now().UnixNano()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
```

The subquery rather than a literal `1`: the id is whatever the migration assigned,
and hard-coding it would break silently if another group were ever seeded first.

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/controller/admin/ -count=1 && go build ./... && go vet ./...`

Expected: PASS and clean. Two kinds of breakage are expected and must be fixed
here rather than worked around:

- a pre-existing test calling `menuItems()` with no argument — the signature
  change is intended;
- any login test still returning 403 — that means Step 5's seed did not take, not
  that the guard is wrong.

- [ ] **Step 7: Commit**

```bash
git add internal/controller/admin/menu.go internal/controller/admin/auth.go \
  internal/controller/admin/admin.go internal/controller/admin/menu_test.go \
  internal/controller/admin/login_test.go
git commit -m "feat(admin): resolve the caller's group once, and filter the menu

Both middlewares now share one resolve-and-inject helper, so the dashboard —
exempt from authorisation — shows the same filtered sidebar a guarded page does.
Specifying filtering only in the guard would have produced a full menu on the
dashboard and a short one everywhere else.

An entry with no resource always shows, which is what AddMenuItem produces for
entries outside the permission model. A user with no group fails closed with 403;
a storage failure is a 500, because an outage must not look like a denial."
```

---

## Task 4: The registrar and the guard

**Files:**
- Modify: `internal/controller/admin/permission.go`
- Test: `internal/controller/admin/permission_test.go`

**Interfaces:**
- Consumes: `permKey`, `recordPermission`, `resolve`, `addResourceMenuItem`, `group`.
- Produces:
  - `func (a *Admin) Resource(eng *inertia.Engine, name string) *Registrar`
  - `func (r *Registrar) Handle(method, path string, h inertia.HandlerFunc)`
  - `func (r *Registrar) GET(path string, h inertia.HandlerFunc)`
  - `func (r *Registrar) POST(path string, h inertia.HandlerFunc)`
  - `func (r *Registrar) Menu(title, path string)`
  - `func (a *Admin) guard(key string) inertia.HandlerFunc`

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/admin/permission_test.go`:

```go
func TestRegistrar_RegistersRoutesAndRecordsKeys(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	noop := func(c *inertia.Context) {}

	r := a.Resource(eng, "post")
	r.GET("/admin/post", noop)
	r.GET("/admin/post/:id/edit", noop)
	r.POST("/admin/post", noop)
	r.POST("/admin/post/:id/delete", noop)
	r.Menu("Post", "/admin/post")

	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}

	got := a.Permissions()
	if len(got) != 2 {
		t.Fatalf("want post.access and post.modify, got %+v", got)
	}
	if got[0].Key != "post.access" || len(got[0].Routes) != 2 {
		t.Errorf("post.access = %+v", got[0])
	}
	if got[1].Key != "post.modify" || len(got[1].Routes) != 2 {
		t.Errorf("post.modify = %+v", got[1])
	}

	// Menu goes through addResourceMenuItem, so it is gated by post.access.
	if len(a.menu) != 1 || a.menu[0].resource != "post" {
		t.Errorf("Menu should record the resource, got %+v", a.menu)
	}
}

// A dot in the name would make permKey produce keys that alias confusingly, and
// this is the one place a name enters the system.
func TestResource_RejectsIllegalNames(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	for _, bad := range []string{"post.access", "Post", "", "my post", "post/sub", ".post"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Resource(%q) should panic", bad)
				}
			}()
			a.Resource(eng, bad)
		}()
	}
	// And a legal one must not panic.
	a.Resource(eng, "blog-post")
}

func TestRegistrar_HandleCoversOtherMethods(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	a.Resource(eng, "post").Handle(http.MethodDelete, "/admin/post/:id", func(c *inertia.Context) {})

	got := a.Permissions()
	if len(got) != 1 || got[0].Key != "post.modify" {
		t.Errorf("DELETE should record post.modify, got %+v", got)
	}
}
```

Add `"github.com/millken/inertia"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/admin/ -run Registrar -count=1`

Expected: FAIL to compile — `undefined: (*Admin).Resource`.

- [ ] **Step 3: Write the implementation**

Append to `internal/controller/admin/permission.go` (add `"regexp"`, `"strconv"` and `"github.com/millken/inertia"` to its imports):

```go
// Registrar registers one resource's admin routes. Every route it registers is
// guarded by that resource's permission, so a route cannot be added without its
// check — which is the failure mode of hand-written permission calls.
type Registrar struct {
	admin    *Admin
	eng      *inertia.Engine
	resource string
}

// Resource returns a registrar for name's routes. name is the permission prefix,
// so "post" yields post.access and post.modify. It is declared rather than
// derived from the path: deriving it would silently re-key every permission when
// someone changes a mount path, revoking access with no error anywhere.
func (a *Admin) Resource(eng *inertia.Engine, name string) *Registrar {
	// permKey and Allows build and split keys textually around ".access" and
	// ".modify", so a name containing a dot would produce keys that alias each
	// other in confusing ways. Rejecting it here is the one place the name enters
	// the system, and a bad name is a wiring mistake — same class as the panic in
	// Handle, and caught at startup rather than in a request.
	if !resourceNameRe.MatchString(name) {
		panic("admin: Resource: illegal resource name " + strconv.Quote(name) +
			" (want ^[a-z0-9][a-z0-9_-]*$)")
	}
	return &Registrar{admin: a, eng: eng, resource: name}
}

// resourceNameRe keeps a resource name free of the separators permission keys are
// built from.
var resourceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Handle registers path for method, guarded by the permission that method
// implies, and records the pairing in the catalogue.
//
// inertia.Engine exposes one method per verb and keeps addRoute unexported, so
// this dispatches rather than passing the method through. An unsupported method
// panics: it can only come from a caller in this repository, and a route silently
// not registered would serve 404 with no clue why.
func (r *Registrar) Handle(method, path string, h inertia.HandlerFunc) {
	key := permKey(r.resource, method)
	guard := r.admin.guard(key)

	// guard alone, never stacked with AuthMiddleware: guard does everything
	// AuthMiddleware does plus the key check, and stacking both would resolve
	// the caller's group twice per request.
	switch method {
	case http.MethodGet:
		r.eng.GET(path, guard, h)
	case http.MethodPost:
		r.eng.POST(path, guard, h)
	case http.MethodPut:
		r.eng.PUT(path, guard, h)
	case http.MethodPatch:
		r.eng.PATCH(path, guard, h)
	case http.MethodDelete:
		r.eng.DELETE(path, guard, h)
	default:
		panic("admin: Registrar.Handle: unsupported method " + method)
	}
	r.admin.recordPermission(key, method, path)
}

// GET registers a read route, guarded by <resource>.access.
func (r *Registrar) GET(path string, h inertia.HandlerFunc) {
	r.Handle(http.MethodGet, path, h)
}

// POST registers a write route, guarded by <resource>.modify.
func (r *Registrar) POST(path string, h inertia.HandlerFunc) {
	r.Handle(http.MethodPost, path, h)
}

// Menu adds the resource's sidebar entry, shown only to callers holding its
// access key.
func (r *Registrar) Menu(title, path string) {
	r.admin.addResourceMenuItem(MenuItem{Title: title, Path: path}, r.resource)
}

// guard requires an authenticated caller whose group holds key. It is the only
// middleware a guarded route needs — resolve already does what AuthMiddleware
// does.
func (a *Admin) guard(key string) inertia.HandlerFunc {
	return func(c *inertia.Context) {
		g, ok := a.resolve(c)
		if !ok {
			return
		}
		if !g.Superuser && !g.Permissions.Allows(key) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
```

`inertia.Engine` offers `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `OPTIONS`, `HEAD`
and `ANY`, with `addRoute` unexported — hence the switch. `OPTIONS`/`HEAD`/`ANY`
are deliberately absent from it: nothing in the generated CRUD uses them, and
`ANY` would make one registration span both verbs, which the key/verb rule cannot
express.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/controller/admin/ -count=1`

Expected: PASS.

- [ ] **Step 5: Add the end-to-end guard test**

This is the test the task exists for, so it drives the real middleware against a
real database rather than calling `Allows` directly. `loginStack` and
`loginAndGetCookie` already exist in `login_test.go`; reuse them.

Append to `internal/controller/admin/permission_test.go`:

```go
// putInGroup moves the harness user into a fresh group with the given flag and
// permission keys, and returns nothing: the caller only cares that the next
// request is evaluated against it.
func putInGroup(t *testing.T, adm *Admin, name string, superuser bool, keysJSON string) {
	t.Helper()
	ctx := context.Background()
	su := 0
	if superuser {
		su = 1
	}
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES (?, ?, ?, 0)`,
		name, su, keysJSON); err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE users SET group_id = (SELECT id FROM user_groups WHERE name = ?) WHERE username = 'alice'`,
		name); err != nil {
		t.Fatalf("move alice into %s: %v", name, err)
	}
}

func TestGuard_EndToEnd(t *testing.T) {
	for _, c := range []struct {
		name      string
		superuser bool
		keysJSON  string
		checkKey  string
		wantCode  int
		wantRun   bool
	}{
		{"superuser passes", true, `[]`, "post.modify", http.StatusOK, true},
		{"key present passes", false, `["post.modify"]`, "post.modify", http.StatusOK, true},
		{"modify implies access", false, `["post.modify"]`, "post.access", http.StatusOK, true},
		{"key absent is forbidden", false, `["post.modify"]`, "billing.modify", http.StatusForbidden, false},
		{"access does not imply modify", false, `["user.access"]`, "user.modify", http.StatusForbidden, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm := loginStack(t)
			putInGroup(t, adm, "TestGroup", c.superuser, c.keysJSON)

			ran := false
			eng.GET("/admin/probe", adm.guard(c.checkKey), func(ic *inertia.Context) { ran = true })
			cookie := loginAndGetCookie(t, eng)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
			r.AddCookie(cookie)
			eng.ServeHTTP(w, r)

			if w.Code != c.wantCode {
				t.Errorf("status = %d, want %d", w.Code, c.wantCode)
			}
			if ran != c.wantRun {
				t.Errorf("handler ran = %v, want %v", ran, c.wantRun)
			}
		})
	}
}

// A user whose group was deleted from under them must be refused, not admitted.
func TestGuard_NoGroupIsForbidden(t *testing.T) {
	eng, adm := loginStack(t)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET group_id = NULL WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	eng.GET("/admin/probe", adm.guard("post.access"), func(ic *inertia.Context) {
		t.Error("handler must not run for a user with no group")
	})
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// The distinction the design turns on: a storage failure is a 500. Reporting it
// as 403 would make an outage look like a permissions decision, and the operator
// would go looking in the wrong place.
func TestGuard_DatabaseErrorIsInternalError(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.guard("post.access"), func(ic *inertia.Context) {
		t.Error("handler must not run when the group cannot be loaded")
	})
	cookie := loginAndGetCookie(t, eng)

	// Close the pool after logging in, so the session resolves but the group
	// query cannot.
	if err := adm.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — a storage failure is not a denial", w.Code)
	}
}

// The dashboard is exempt from authorisation, so a user holding nothing still
// reaches it. Its sidebar is filtered, which is the intended failure mode: a
// short menu rather than a wall.
func TestExemptRoute_ReachableWithoutPermissions(t *testing.T) {
	eng, adm := loginStack(t)
	putInGroup(t, adm, "Nobody", false, `[]`)

	ran := false
	eng.GET("/admin/exempt", adm.AuthMiddleware(), func(ic *inertia.Context) { ran = true })
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/exempt", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if !ran {
		t.Errorf("an exempt route must be reachable with no permissions; status %d", w.Code)
	}
}
```

Add `"context"`, `"net/http/httptest"` and `"testing"` to the file's imports as
needed; `net/http` and `github.com/millken/inertia` are already there from Step 1.

Note the inner handler parameter is named `ic` rather than `c`, because `c` is the
table-driven case variable in `TestGuard_EndToEnd`.

- [ ] **Step 6: Run the suite and commit**

Run: `go test ./internal/controller/admin/ -count=1 && gofmt -l internal/`

Expected: PASS, no gofmt output.

```bash
git add internal/controller/admin/permission.go internal/controller/admin/permission_test.go
git commit -m "feat(admin): the registrar and the permission guard

One call registers a route, attaches the guard bound to the key its method
implies, and records the pairing — so a route cannot be registered without its
check, which is exactly the hole hand-written permission calls leave.

guard is attached alone rather than alongside AuthMiddleware: it already does
everything AuthMiddleware does, and stacking both would resolve the caller's
group twice per request."
```

---

## Task 5: `admin create-user --group`

Without this the seeded superuser group has no members and the admin area is
unreachable.

**Files:**
- Modify: `commands/admin_user.go`
- Test: `commands/admin_user_test.go` (create if absent; check first)

**Interfaces:**
- Consumes: the `user_groups` table from Task 2.
- Produces: `admin create-user <username> --group <name>`, defaulting to `Administrators`.

- [ ] **Step 1: Read the command**

Read `commands/admin_user.go`. It currently builds the insert as:

```go
q := fmt.Sprintf(`INSERT INTO %s (username, password_hash, created_at) VALUES (?, ?, ?)`, table)
if _, err := dbSvc.DB().ExecContext(ctx, q, username, hash, time.Now().UnixNano()); err != nil {
```

- [ ] **Step 2: Put the group lookup in the admin package, where it can be tested**

The command layer has no database test harness, and adding one for eight lines of
SQL is not worth it — but the lookup does need a test, because "missing group" is
the error the operator will actually hit. So the SQL goes next to the rest of the
package's SQL instead.

Append to `internal/controller/admin/group.go`:

```go
// FindGroupID resolves a group name to its id. Exported for
// `myapp admin create-user --group`, which must fail on an unknown name rather
// than leave a user with a null group_id — such a user logs in successfully and
// is then refused everything, which reads as a bug rather than a misconfiguration.
func FindGroupID(ctx context.Context, d *sqldb.DB, name string) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no permission group named %q — the migration seeds "+
			"'Administrators'; pass --group with an existing name", name)
	}
	if err != nil {
		return 0, fmt.Errorf("admin: look up group %q: %w", name, err)
	}
	return id, nil
}
```

Append to `internal/controller/admin/group_test.go`:

```go
func TestFindGroupID(t *testing.T) {
	d := groupFixture(t)
	ctx := context.Background()

	id, err := FindGroupID(ctx, d, "Editors")
	if err != nil {
		t.Fatalf("Editors: %v", err)
	}
	if id != 2 {
		t.Errorf("Editors id = %d, want 2", id)
	}

	_, err = FindGroupID(ctx, d, "Nope")
	if err == nil {
		t.Fatal("want an error for an unknown group")
	}
	// The message has to name both what was asked for and what exists, because
	// this is the error an operator meets when bootstrapping.
	for _, want := range []string{"Nope", "Administrators", "--group"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}
```

Add `"strings"` to `group_test.go`'s imports.

- [ ] **Step 3: Add the flag and use it**

In `commands/admin_user.go`, add alongside the existing `password` variable:

```go
	var groupName string
```

Register it next to the existing `--password` flag:

```go
	cmd.Flags().StringVar(&groupName, "group", "Administrators",
		"Permission group to place the user in")
```

Resolve the group before the insert — it must fail before a user row exists, not
after:

```go
			groupID, err := admin.FindGroupID(ctx, dbSvc.DB(), groupName)
			if err != nil {
				return err
			}
```

And extend the insert:

```go
			q := fmt.Sprintf(`INSERT INTO %s (username, password_hash, created_at, group_id)
				VALUES (?, ?, ?, ?)`, table)
			if _, err := dbSvc.DB().ExecContext(ctx, q, username, hash, time.Now().UnixNano(), groupID); err != nil {
```

The file already imports the `admin` package (for `admin.HashPassword`), so no
import changes are needed.

- [ ] **Step 4: Verify by hand against a scratch database**

```bash
cd /workspace/Codes/github.com/millken/goapp-template
rm -f /tmp/perm.db
MYAPP_HOME=. go run . admin create-user alice --password secret 2>&1 | tail -2
```

Expected: `created admin user "alice"`. Then confirm the group landed:

```bash
go run . admin create-user bob --password x --group Nope 2>&1 | tail -2
```

Expected: a non-zero exit naming `Nope` and mentioning `Administrators`.

Note: this writes to the repo's gitignored `app.db` via `config.yaml`. Leave it;
it is gitignored and rebuilt freely.

- [ ] **Step 5: Run the suite and commit**

Run: `go build ./... && go vet ./... && go test ./... -count=1`

```bash
git add commands/admin_user.go internal/controller/admin/group.go \
  internal/controller/admin/group_test.go
git commit -m "feat(admin): create-user takes --group, defaulting to Administrators

The group is resolved before the insert so a missing name is a clear error rather
than a user with a null group_id — which would log in successfully and then be
refused everything, reading as a bug instead of a misconfiguration.

Defaulting to the seeded superuser group is safe: running this command already
requires shell access, and the alternative is an operator locked out of the admin
area they just created."
```

---

## Task 6: The generated template uses the registrar

**Files:**
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl`
- Modify: `frontend/pages/admin/ssrfixture/index.vue` (regenerate)
- Test: `cmd/goappctl/internal/scaffold/admin_test.go`

**Interfaces:**
- Consumes: `adm.Resource(eng, name)` and the registrar from Task 4.
- Produces: generated `Mount` functions that register through the registrar.

- [ ] **Step 1: Replace `Mount` in the template**

In `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl`, replace the
whole `Mount` function with:

```go
// Mount registers the admin [[.Type]] routes and adds a menu entry. Each route
// is guarded by the permission its method implies — [[.Route]].access for reads,
// [[.Route]].modify for writes — so a route cannot be added without its check.
func Mount(eng *inertia.Engine, svc *app.Services, adm *admin.Admin) {
	ct := &Controller{Services: svc, base: adm.Prefix() + "/[[.Route]]"}
	r := adm.Resource(eng, "[[.Route]]")

	r.GET(ct.base, ct.Index)
	r.GET(ct.base+"/new", ct.New)
	r.POST(ct.base, ct.Create)
	r.GET(ct.base+"/:id/edit", ct.Edit)
	r.POST(ct.base+"/:id", ct.Update)
	r.POST(ct.base+"/:id/delete", ct.Delete)
	r.Menu("[[.Type]]", ct.base)
}
```

The `auth := adm.AuthMiddleware()` line goes, and so does `adm.AddMenuItem(...)`
at the end of the old `Mount`.

- [ ] **Step 2: Write the failing content test**

Append to `cmd/goappctl/internal/scaffold/admin_test.go`:

```go
func TestAdmin_RoutesGoThroughTheRegistrar(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/controller/adminpost/handler.go"))
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		`r := adm.Resource(eng, "post")`,
		"r.GET(ct.base, ct.Index)",
		"r.POST(ct.base+\"/:id/delete\", ct.Delete)",
		`r.Menu("Post", ct.base)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("handler.go missing %q:\n%s", want, got)
		}
	}
	// A route registered outside the registrar has no permission check, which is
	// the hole this design exists to close.
	for _, gone := range []string{"adm.AuthMiddleware()", "eng.GET(", "eng.POST(", "adm.AddMenuItem("} {
		if strings.Contains(got, gone) {
			t.Errorf("handler.go still contains %q — routes must go through the registrar:\n%s", gone, got)
		}
	}
}
```

- [ ] **Step 3: Run it**

Run: `go test ./cmd/goappctl/internal/scaffold/ -run TestAdmin_RoutesGoThrough -count=1`

Expected: PASS with Step 1 applied.

- [ ] **Step 4: Regenerate the SSR fixture**

The fixture tracks `admin/index.vue.tmpl`, which this task does not change — but
run the staleness check to be sure, and regenerate if it fails:

Run: `go test ./cmd/goappctl/internal/scaffold/ -run TestAdminIndexFixtureIsCurrent -count=1`

Expected: PASS. If it fails, regenerate with the steps in the comment at the top
of `frontend/pages/admin/ssrfixture/index.vue`.

- [ ] **Step 5: Verify generated output compiles**

```bash
go test ./cmd/goappctl/internal/scaffold/ -run OutputCompiles -count=1
go run ./cmd/goappctl gen admin permcheck --force
go build ./internal/controller/adminpermcheck/
rm -rf internal/controller/adminpermcheck frontend/pages/admin/permcheck
git checkout internal/controller/mount_gen.go
```

Expected: both build steps exit 0, and `git status --short` is clean afterwards.

- [ ] **Step 6: Commit**

```bash
git add cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl \
  cmd/goappctl/internal/scaffold/admin_test.go
git commit -m "feat(goappctl): generated admin routes register through the registrar

One call per route now registers it, guards it with the permission its method
implies, and records the pairing in the catalogue. The test asserts the absence of
eng.GET/eng.POST/AuthMiddleware as well as the presence of the registrar calls:
a route registered the old way would compile and serve with no permission check,
which is precisely the hole this closes."
```

---

## Task 7: Document the model

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a section after 「后台 UI 组件」**

Inside the existing `<!--goappctl:admin-->` block — do not add, remove or reorder
any marker line; `goappctl init` errors on an unbalanced one:

````markdown
## 后台权限

权限单元是**资源 + 动词**：`post.access`（读）和 `post.modify`（写）。动词由 HTTP 方法决定 ——
GET/HEAD 是 `access`，其余是 `modify` —— 所以没有任何路由需要自己声明权限。

`modify` **蕴含** `access`：能改的人当然能读。反向不成立。

生成的 `Mount` 通过注册器登记路由，一次调用同时做三件事：注册、挂上守卫、记入权限目录。

```go
r := adm.Resource(eng, "post")
r.GET(ct.base, ct.Index)                 // 需要 post.access
r.POST(ct.base+"/:id", ct.Update)        // 需要 post.modify
r.Menu("Post", ct.base)                  // 侧边栏条目，受 post.access 控制
```

**守卫就是路由中间件**，注册即生效 —— 这是它相对手写 `if hasPermission(...)` 的关键差别：
后者漏一处就是静默的洞，前者漏不掉，因为没有不经注册器的注册路径。

权限存在分组上：`user_groups.permissions` 是一个 JSON 键数组，`superuser = 1` 直接放行。
迁移会播种一个 `Administrators` 超管组，`admin create-user` 默认把用户放进去：

```bash
myapp admin create-user alice                      # 进 Administrators（超管）
myapp admin create-user bob --group Editors        # 进指定组
```

**豁免路由**（登录后无条件放行）：登录页、登出、以及 `/admin` 仪表盘。仪表盘必须豁免 ——
否则权限为空的用户登录后只看到 403，无法自助。侧边栏会按权限过滤，所以他看到的是一个
短菜单而不是一堵墙。

第一期没有分组管理界面，权限集用 SQL 设定：

```sql
UPDATE user_groups SET permissions = '["post.access","post.modify"]' WHERE name = 'Editors';
```

`adm.Permissions()` 返回全部已注册的权限键及各自覆盖的路由，供将来的权限编辑界面使用。

**`users_table` 只影响运行时查询。** 迁移操作字面量 `users` 表（嵌入的 SQL 读不到配置），
所以把它指向别的表意味着那张表的结构由你负责，包括 `group_id` 列。
````

- [ ] **Step 2: Verify markers balance and commit**

```bash
printf "openers=%s closers=%s\n" \
  "$(grep -c '<!--goappctl:\(admin\|ssr\|db\|session\|tooling\)-->' README.md)" \
  "$(grep -c '<!--goappctl:end-->' README.md)"
go test ./cmd/goappctl/... -count=1
```

Expected: the two counts match, and the suite passes.

```bash
git add README.md
git commit -m "docs: the admin permission model

Resource plus verb, the verb from the HTTP method, and modify implying access one
way. Records why the dashboard is exempt (a permission system's failure mode
should be a short menu, not a wall) and that users_table redirects runtime
lookups only."
```

---

## Final verification

- [ ] **Step 1: Everything green**

Run: `go build ./... && go vet ./... && gofmt -l cmd/ internal/ server/ commands/ && go test ./... -count=1 && pnpm -C frontend type-check`

Expected: all exit 0, `gofmt -l` silent.

- [ ] **Step 2: A trimmed project still builds**

The admin component owns all of this, so a project without it must not carry any
of it:

```bash
TMP=$(mktemp -d); git archive HEAD | tar -x -C "$TMP"
cd "$TMP" && rm -f go.work go.work.sum
go run ./cmd/goappctl init --module github.com/me/nogroups --with db --force --dry-run \
  | grep -E "internal/controller/admin|003_user_groups"
cd - && rm -rf "$TMP"
```

Expected: `internal/controller/admin` is deleted. Note that
`internal/service/db/migrations/003_user_groups.up.sql` is **not** deleted — the
migrations directory belongs to the `db` component, so a db-only project keeps a
migration that alters a `users` table it still has (from `002`) and creates a
`user_groups` table nothing reads. Report that as an observation; deciding whether
the migration should move under `admin` ownership is a design question, not this
plan's to settle.

- [ ] **Step 3: End-to-end by hand**

```bash
rm -f app.db
MYAPP_HOME=. go run . admin create-user root --password root
make dev
```

Then sign in at `http://localhost:8080/admin` as `root`/`root`. Expected: the
dashboard renders and the sidebar shows every generated resource, because
`Administrators` is a superuser group.

To see a denial, create a second group with no permissions and move a user into
it:

```sql
INSERT INTO user_groups (name, superuser, permissions, created_at)
VALUES ('Nobody', 0, '[]', 0);
UPDATE users SET group_id = (SELECT id FROM user_groups WHERE name='Nobody')
WHERE username='root';
```

Expected after re-login: the dashboard still renders, the sidebar is empty, and
visiting a resource URL directly returns 403.
