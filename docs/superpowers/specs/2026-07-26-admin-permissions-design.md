# Admin Permissions — Design Spec

Date: 2026-07-26 · Status: Validated — approved for planning · Depends on: the
`admin` component (session + db)

## 1. Goal

Give the admin area per-resource permissions that appear automatically as
resources are generated, and that cannot be forgotten at a call site.

Today `admin.AuthMiddleware()` answers one question — is anyone logged in — and
every admin route gets the same answer. There is no way to let one person manage
posts but not users. The model borrowed here is OpenCart's: the unit of
permission is a resource with two verbs, `access` (read) and `modify` (write),
and the permission a route needs is decided by the route itself rather than by a
taxonomy someone maintains by hand.

Where this improves on OpenCart: its checks are hand-written
`if (!$this->user->hasPermission('modify', 'catalog/product'))` calls scattered
through controllers, so a missing check is a silent hole. Here the check *is* the
route middleware, attached at registration, so a route cannot be registered
without one.

## 2. Decisions

| Question | Decision |
|---|---|
| Unit of permission | **Resource + verb** — `post.access`, `post.modify`. Not per-route |
| Verb from method | GET/HEAD → `access`; everything else → `modify` |
| `modify` vs `access` | **`modify` implies `access`.** A group that may POST may obviously GET |
| Resource name | **Declared explicitly once**: `adm.Resource(eng, "post")` |
| Permissions belong to | **Groups.** New `user_groups` table; `users` gains `group_id` |
| Storage | **A JSON array of keys** on the group row, not a join table |
| Superuser | A **`superuser` boolean column** on the group, not a magic id |
| Permission lookup | **One indexed query per admin request.** Not cached in the session |
| Exempt routes | login (already public), logout, and the dashboard |
| Denial | `403` via `c.AbortWithStatus`, matching how the engine handles 404 |
| Management UI | **Out of scope** — stage 2 (§11) |

**Why the name is declared, not derived.** Deriving it from the mount path
(`/admin/post` → `post`) is tempting and needs no argument, but it silently
re-keys every permission when someone changes a path: a group that could modify
`post` now has a permission nothing checks, and loses access without any error.
Deriving it from the package name avoids that but needs `runtime.Caller` or
reflection to read it. One explicit string, written once by the generator, costs
nothing and fails loudly if wrong (the key simply is not in the catalogue).

**Why the key is captured at registration, not derived per request.** The obvious
implementation reads the matched route pattern inside the middleware — but
`inertia.Context` has no `FullPath()`; its exported surface offers
`Request.URL.Path` (the *concrete* path, `/admin/post/42/edit`) and `Params`.
Nothing exposes `/admin/post/:id/edit`, and normalising by stripping id-shaped
segments is guesswork. `inertia.Engine` likewise cannot enumerate its routes. Its `router` field is
unexported, and `router.Router[T]` offers only `Add`, `Lookup`, `LookupNoAlloc`
and `Map` — as do the seven per-method `Tree[T]` values it composes, which are
themselves unexported fields. `Map` transforms handlers in place and yields no
paths. There is no traversal entry point at any level. So the design captures `(resource, verb)` in a closure
when the route is registered. This needs no change to the `inertia` dependency
and yields the enumerable catalogue a management UI needs as a by-product.

**Why groups rather than a permission set per user.** Two people in the same job
should not have their permissions ticked twice, and a group gives the superuser
concept somewhere natural to live. The cost is one more table and, in stage 2, one
more CRUD screen.

**Why a JSON array, not a join table.** The set is read whole on every request
and never queried by permission — nobody asks "which groups may modify posts?"
outside a screen that edits one group at a time. A join table would add a join
for no query it enables. Keys are stored flat (`["post.access","post.modify"]`)
rather than in OpenCart's `{"access":[…],"modify":[…]}` shape, so there is one
representation and no mapping between the stored form and the checked form.

**Why not cache permissions in the session.** It would remove the per-request
query, but a revoked permission would keep working until the user next logged in.
Stale authorisation is a security smell, and the cost avoided is one indexed read
on admin traffic.

## 3. Non-goals

- **A management UI.** No group CRUD, no permission editor. Stage 2 (§11). Until
  then a permission set is written with one `UPDATE`, and the seeded superuser
  group covers the common case.
- **Per-route permissions.** `POST /admin/post/:id` and
  `POST /admin/post/:id/delete` share `post.modify`. "May edit but not delete"
  is not expressible, deliberately: it would put six checkboxes per resource in
  front of the operator instead of two.
- **Permissions on public routes.** `templates/resource/*` and everything under
  `internal/controller/site/` are untouched. Public resources have no session.
- **Role inheritance, group nesting, per-record rules.** A group holds a flat set.
- **Changing what `adminUser` means.** `AuthMiddleware` currently sets that prop
  to the user's numeric id, so the dashboard reads "Signed in as 1". The guard
  will be loading the user row anyway, which would make showing the username
  nearly free — but changing a prop's meaning has its own blast radius across
  every admin page, and does not belong in the same change as authorisation.
  Recorded so it is not rediscovered as a mystery.

## 4. The registrar

`Admin` gains one method returning a small registrar. Each registration does
three things at once: registers the route, attaches a guard already bound to its
permission key, and records the key in a catalogue.

```go
// Resource returns a registrar whose routes are guarded by name's permissions.
// The name is the permission prefix, so "post" yields post.access / post.modify.
func (a *Admin) Resource(eng *inertia.Engine, name string) *Registrar

type Registrar struct { /* admin, engine, resource name */ }

// Handle registers path for method, guarded by the permission the method implies.
func (r *Registrar) Handle(method, path string, h inertia.HandlerFunc)

// GET and POST are the two the generated CRUD uses; Handle covers the rest.
func (r *Registrar) GET(path string, h inertia.HandlerFunc)
func (r *Registrar) POST(path string, h inertia.HandlerFunc)

// Menu adds the resource's sidebar entry, as AddMenuItem does today.
func (r *Registrar) Menu(title, path string)
```

A generated `Mount` becomes:

```go
func Mount(eng *inertia.Engine, svc *app.Services, adm *admin.Admin) {
	ct := &Controller{Services: svc, base: adm.Prefix() + "/post"}
	r := adm.Resource(eng, "post")

	r.GET(ct.base, ct.Index)                 // post.access
	r.GET(ct.base+"/new", ct.New)            // post.access
	r.POST(ct.base, ct.Create)               // post.modify
	r.GET(ct.base+"/:id/edit", ct.Edit)      // post.access
	r.POST(ct.base+"/:id", ct.Update)        // post.modify
	r.POST(ct.base+"/:id/delete", ct.Delete) // post.modify
	r.Menu("Post", ct.base)
}
```

The `auth := adm.AuthMiddleware()` line disappears from generated code: a route
registered through the registrar is guarded by construction.

**The catalogue** is what makes the set enumerable, and it records which routes
each key covers so a future screen can explain itself rather than showing bare
strings:

```go
// Permissions returns every registered key with the routes it guards, sorted by
// key. Built during Mount, read-only afterwards.
func (a *Admin) Permissions() []Permission

type Permission struct {
	Key    string   // "post.modify"
	Routes []string // "POST /admin/post", "POST /admin/post/:id", …
}
```

## 5. The guard

`AuthMiddleware()` keeps its current meaning — *authenticated*, nothing more —
and is what the exempt routes use. Guarded routes get `guard(key)` **instead**,
never in addition:

```go
// what the registrar attaches — one middleware, not two
eng.GET(path, a.guard("post.access"), handler)
```

That matters for §2's "one indexed query per admin request". `guard` does
everything `AuthMiddleware` does *plus* the key check, so stacking both would
resolve the group twice per request and make that claim false. Because exactly one
of the two runs on any given route, no caching in `c.Set` is needed to keep the
count at one — the simpler structure is also the correct one.

The middleware itself:

```go
// guard requires an authenticated user whose group holds key.
func (a *Admin) guard(key string) inertia.HandlerFunc
```

On each request it resolves the session's user id to a group:

```sql
SELECT g.superuser, g.permissions
FROM <users_table> u
JOIN user_groups g ON g.id = u.group_id
WHERE u.id = ?
```

Then, in order:

1. No session id → redirect to login (identical to `AuthMiddleware` today).
2. Query error → `500`, logged. **Not** treated as "no permission": a database
   problem must not read as an authorisation decision.
3. No group row (`group_id` null, or a group deleted from under the user) →
   `403`. Failing closed is right; a user with no group has no permissions.
4. `superuser = 1` → allow.
5. Key present in the group's set → allow. For an `access` key, the matching
   `modify` key also allows it (§2's implication rule).
6. Otherwise → `403`.

### 5.1 Shared props and menu filtering live in one place

Both middlewares inject the props admin pages expect — `adminMenu`, `adminUser`,
`adminMount`, `loginPath` — and **both filter the menu to entries the user may
access**. A sidebar offering links that all 403 is worse than a short sidebar.

That filtering is why the group lookup is not confined to `guard`: the dashboard
is exempt from *authorisation* but still needs a correctly filtered menu, so
`AuthMiddleware` performs the same lookup. The two differ in exactly one respect —
`guard` additionally requires a key. Concretely, one unexported helper resolves
the group and injects the props, and both middlewares call it; neither owns a
copy.

A menu entry is filtered out when its resource's `access` key is absent, so
filtering needs each entry to know its resource. `MenuItem` gains nothing: the
menu store keeps the association beside the item.

```go
// menuEntry pairs a sidebar item with the resource whose access key gates it.
// An empty resource means "always show" — that is what AddMenuItem produces, for
// hand-written entries that guard nothing.
type menuEntry struct {
	item     MenuItem
	resource string
}
```

`Registrar.Menu` records the entry with its resource; `AddMenuItem` stays exactly
as it is, for entries outside the permission model.

## 6. Schema and storage

New migration `003_user_groups.up.sql` / `.down.sql`, following the existing
files' SQLite-flavoured style with dialect notes in comments.

**Up:**

```sql
CREATE TABLE IF NOT EXISTS user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    superuser   INTEGER NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
);

ALTER TABLE users ADD COLUMN group_id INTEGER REFERENCES user_groups(id);

INSERT INTO user_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]',
        CAST(strftime('%s', 'now') AS INTEGER) * 1000000000);
```

**Down:**

```sql
ALTER TABLE users DROP COLUMN group_id;
DROP TABLE IF EXISTS user_groups;
```

`permissions` holds a JSON array of keys — `["post.access","post.modify"]`.
`created_at` is UnixNano, matching the `users` and `sessions` convention — hence
the `strftime` multiplication, which yields second precision in nanosecond units.

### 6.1 The down migration is clean, and that was verified

`DROP COLUMN` is the part worth checking rather than assuming: SQLite only gained
it in 3.35 (2021), and it refuses columns that are indexed, unique, or part of a
primary key. `group_id` carries a `REFERENCES` clause, which is the case most
likely to be refused.

Tested against the driver this template actually uses: `mattn/go-sqlite3` bundles
**SQLite 3.53.3**, and `ALTER TABLE users DROP COLUMN group_id` succeeds on a
column declared with `REFERENCES user_groups(id)`. MySQL and PostgreSQL both
support `DROP COLUMN` unconditionally. So the down migration rolls back cleanly on
all three dialects the template names, and no table-rebuild dance is needed.

Order matters: the column goes before the table it references.

### 6.2 Migrations run once, in a transaction — so nothing here is idempotent

`sqldb`'s migrator records a version and wraps each migration file in
`BeginTx` … `Commit`, rolling back on error or panic. Each file therefore applies
exactly once and atomically.

This is why the seed is a plain `INSERT` rather than a guarded
`INSERT … WHERE NOT EXISTS`: a guard would imply the statement might run twice,
which the migrator prevents. It also settles `ALTER TABLE ADD COLUMN`, which is
*not* idempotent — a second run fails with `duplicate column name: group_id`
(verified) — but never gets a second run. Re-applying this schema to a database
out of band is outside the migrator's contract, and pretending otherwise with
partial guards would be worse than saying so.

### 6.3 `users_table` is configurable; the migration is not — and that is a real limit

The guard's join interpolates the configured `users_table` (§5), but this
migration writes the literal `users`. Migrations are embedded at build time
(`//go:embed migrations/*.sql` in `internal/service/db/db.go`), so no
configuration value can reach them.

**This contradiction predates this spec.** `002_users.up.sql` already does
`CREATE TABLE IF NOT EXISTS users` while `admin.users_table` claims the table is
configurable. This design inherits it and, by adding an `ALTER`, makes it sharper —
so it gets stated rather than left for an implementer to discover:

- Migrations own the literal `users` table.
- `users_table` therefore redirects **runtime lookups only** — login, and the
  guard's join.
- Pointing it at another table means the operator owns that table's schema,
  including adding a `group_id` column compatible with the join. The template
  will not do it for them, and the failure is a SQL error at login rather than
  anything friendlier.

That narrows what `users_table` is good for: adopting a table whose *shape* you
have already matched, not a free-form redirect. Widening it would mean either
templating the migration SQL or moving group membership out of the users table
entirely — both larger changes than this design, and neither needed to ship it.

**The groups table name is not configurable**, and that asymmetry is deliberate:
`user_groups` is created here and has no pre-existing counterpart to point at, so
a second knob would be speculative. The `users_table` value stays subject to the
existing `^[A-Za-z_]\w*$` guard before interpolation.

## 7. Exemptions and bootstrap

**Exempt** — authenticated but requiring no permission:

| Route | Why |
|---|---|
| `GET/POST <login>` | Already public; unchanged |
| `POST /admin/logout` | Nobody should be unable to leave |
| `GET /admin` (dashboard) | Otherwise a user with no permissions logs in, sees only 403, and cannot self-diagnose |

The dashboard exemption means any authenticated user reaches the shell and the
sidebar. They see the frame and whichever menu entries they may use — which is
the point: the failure mode of a permission system should be a short menu, not a
wall.

**Bootstrap.** The migration seeds one group, `Administrators`, with `superuser = 1` and an empty permission set —
the `INSERT` in §6. It is stated once there rather than repeated here, so the two
cannot drift.

`admin create-user` gains `--group <name>`, defaulting to `Administrators`, and
fails with a clear message if the named group does not exist. Defaulting to the
superuser group is safe: running that command already requires shell access to
the server, and the alternative — a first user with no permissions — locks the
operator out of their own admin area.

## 8. Denial behaviour

`c.AbortWithStatus(http.StatusForbidden)`, which is how the engine surfaces 404
today. No styled 403 page: that is interface work and belongs with stage 2's
screens. Under PJAX the 403 surfaces as a failed navigation, which is honest —
the request genuinely was refused.

## 9. Files touched

| File | Change |
|---|---|
| `internal/controller/admin/permission.go` | New: `Registrar`, `Resource`, `guard`, `Permissions`, the key/verb rules |
| `internal/controller/admin/permission_test.go` | New |
| `internal/controller/admin/admin.go` | `Admin` gains the catalogue field; `Mount` unchanged (its routes are exempt) |
| `internal/controller/admin/auth.go` | `AuthMiddleware` keeps its meaning but now also resolves the group, so the menu it injects is filtered; the resolve-and-inject helper is shared with `guard` rather than copied |
| `internal/controller/admin/menu.go` | The menu store keeps each entry's resource, so filtering has something to filter on |
| `internal/controller/admin/group.go` | New: the group lookup and the JSON permission set |
| `internal/service/db/migrations/003_user_groups.up.sql` | New |
| `internal/service/db/migrations/003_user_groups.down.sql` | New |
| `commands/admin_user.go` | `--group`, defaulting to `Administrators` |
| `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` | `Mount` uses the registrar; the `auth :=` line goes |
| `frontend/pages/admin/ssrfixture/index.vue` | Regenerated (the fixture tracks that template) |
| `README.md` | The permission model, the two verbs, the exempt routes, bootstrap |

`templates/resource/*`, `internal/controller/site/**` and every public route are
untouched.

## 10. Testing

- **Key derivation**: `GET` yields `<name>.access`, `POST` yields
  `<name>.modify`; `Handle` covers `PUT`/`PATCH`/`DELETE` as `modify`.
- **The implication rule**: a group holding only `post.modify` passes a
  `post.access` check. A group holding only `post.access` fails a `post.modify`
  check — the implication is one-way, and a test that only checks the first
  direction would pass for an implementation that allowed everything.
- **The catalogue**: keys deduplicate across routes; `Routes` lists every route a
  key guards; output is sorted so a UI is stable.
- **The guard**, against `sqldb.Open("sqlite3", ":memory:")` as
  `store_test.go` does: superuser allowed; key present allowed; key absent 403;
  no session redirects to login; **a query error is a 500, not a 403**; a user
  whose group was deleted gets 403.
- **Exempt routes** reachable by a user whose group holds no permissions —
  dashboard renders, logout works.
- **Menu filtering**: an entry whose resource the user cannot access is absent
  from `adminMenu` — asserted on **both** an exempt route (the dashboard) and a
  guarded one, since the two middlewares must agree.
- **The always-show convention**, asserted on `menuEntry{resource: ""}` directly
  rather than through `AddMenuItem`. `AddMenuItem` is only one way to produce an
  empty resource; testing it instead of the convention would leave the convention
  itself unpinned, and a later second producer could regress without failing
  anything. One further case covers that `AddMenuItem` does produce an empty
  resource.
- **Migration**: after Start, `Administrators` exists with `superuser = 1`, and
  `users.group_id` exists. Then **Start a second time on the same database and
  assert it succeeds** — `ALTER TABLE ADD COLUMN` is not idempotent, so this pins
  the property the design actually relies on (the migrator applies each file once,
  §6.2) rather than testing the SQL for an idempotency it does not have. A
  regression in version tracking would surface here as
  `duplicate column name: group_id`.
- **Down migration**: `MigrateTo` back past `003` drops the column and the table,
  and a subsequent up re-applies cleanly. This is the only test of §6.1's claim,
  which was verified by hand but is otherwise unguarded.
- **`admin create-user --group`**: assigns the named group; a missing group is a
  clear error, not a user with a null group.
- **Generated code compiles**: `TestAdmin_OutputCompiles` already renders the
  admin templates into the real module tree and builds them.
- **The SSR fixture stays current**: `TestAdminIndexFixtureIsCurrent` fails until
  the fixture is regenerated after the template change.

## 11. Stage 2 (deferred)

Group CRUD and a permission editor, as admin resources built on the components
from the admin-UI work: a group list, a form, and a permission matrix rendered
from `adm.Permissions()` — which is why the catalogue records routes per key.
That screen is the reason this design keeps the permission set enumerable, but it
needs none of it to be written first, and stage 1 is verifiable without it.
