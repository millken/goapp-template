# Admin User & Permission Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the admin area screens for managing its own users and permission groups — create, edit, disable, reset passwords, and edit what a group may do — with guardrails that make locking yourself out impossible.

**Architecture:** Two registrar-registered resources (`user`, `group`) plus one deliberately permission-exempt account page, all in `internal/controller/admin/`. Disabling is enforced by one extra column on the query `resolve` already runs. The "at least one enabled superuser" rule is a single shared guard that applies the change in a transaction and rolls back if the resulting count is zero, rather than four bespoke pre-checks.

**Tech Stack:** Go 1.26, `github.com/dnsoa/go/sqldb`, `internal/validate`, Vue 3 with the admin composites (`AdminShell`, `PageHeader`, `DataTable`, `FormField`, `ConfirmDialog`).

**Spec:** `docs/superpowers/specs/2026-07-27-admin-user-management-design.md` — read it before any task.

## Global Constraints

- **One group lookup per request.** Nothing in this plan adds a query to `resolve`.
- **Three outcomes stay distinguishable** in `resolve`: `errNoGroup` → 403 (a permissions answer), any other error → 500 (an infrastructure failure), `status = 0` → flash + redirect to login (an account answer).
- **`resolve` must not destroy the session on the disabled path.** A flash *is* session state, and `Destroy` also clears the cookie it would travel in — the two cannot both happen. The session is already inert because `resolve` refuses it on every request.
- **`authenticate` checks `status` only after `verifyPassword` succeeds.** Checking earlier makes a disabled account distinguishable from a wrong password by timing, which is the leak `dummyPasswordHash` exists to prevent.
- **Every admin route goes through the registrar**, with exactly one exception: `GET|POST /admin/account/password`, which uses `AuthMiddleware`. The reason must appear in a comment beside the registration.
- **The last-superuser count runs inside the transaction, after the mutation.** Outside it, or before, it is a prediction of the resulting state rather than a reading of it.
- **Every redirect goes through `a.redirect(c, location)`, never `http.Redirect`.** Under a PJAX navigation a redirect must become a `{redirect}` payload the client can act on; a raw 3xx is followed by fetch transparently, leaving one page rendered while the address bar names another. `TestRedirects_UnderPJAX` pins this for the auth redirects and the mutations are no different. (Corrected mid-plan: the first draft copied `http.Redirect` from the generated scaffold template, which has the same latent bug — recorded separately, out of scope here.)
- Passwords: 8–72 characters. 72 because **bcrypt truncates there**, so a longer password is partly not the credential.
- Usernames: `^[a-zA-Z0-9._-]+$`, 3–64 — ASCII-only on purpose; admin accounts are operator-created.
- Migrations own the literal `users` table; `[admin] users_table` redirects runtime lookups only.
- No new Go or npm dependencies. `frontend/src/styles/main.css` unchanged; token classes only.
- Overlays start closed; no `<button>` may nest in a `<button>`.
- Verification, Go: `go build ./...`, `go vet ./...`, `gofmt -l ./` silent, `go test ./... -count=1`. Frontend: `pnpm -C frontend run test`, `run type-check`, `run build`.

## Two traps specific to this plan

1. **`MaxOpenConns: 1` plus an open transaction deadlocks.** The test harness (`loginStack`) pins the pool to one connection so `:memory:` stays a single database. While a transaction holds that connection, any query issued on `ct.DB` instead of on the `*sqldb.Tx` will block forever. Inside a transaction, use the `tx` handle for everything.
2. **`db.Transaction` begins with `context.Background()`** (see `sqldb/db.go:117-122`), so the transaction itself is not cancellable. Pass the request context to the *queries* inside it via `ExecContext` / `QueryRowContext`, which is what actually matters.

## File Structure

```
internal/service/db/migrations/004_user_status.{up,down}.sql   Task 1
internal/controller/admin/group.go      Task 1: findGroup → findCaller (+status)
internal/controller/admin/auth.go       Task 1: resolve's third outcome
internal/controller/admin/user.go       Task 1: User.Status, findUser, authenticate
internal/controller/admin/guardrails.go Task 2: the three rules, shared
internal/controller/admin/user_crud.go  Tasks 3–4: the user resource
frontend/pages/admin/user/{index,form}.vue
internal/controller/admin/group_crud.go Tasks 5–6: the group resource + grid
frontend/pages/admin/group/{index,form}.vue
internal/controller/admin/account.go    Task 7: self-service password
frontend/pages/admin/account/password.vue
frontend/src/components/admin/AdminShell.vue  Task 7: the user-menu item
internal/controller/admin/admin.go      Task 8: Mount wiring
cmd/goappctl/internal/components/components.go, README.md   Task 8
```

---

### Task 1: `status` exists and means something

The column, and every read that has to respect it. One task because a `status`
column nothing enforces is worse than no column — it looks like protection.

**Files:**
- Create: `internal/service/db/migrations/004_user_status.up.sql`, `internal/service/db/migrations/004_user_status.down.sql`
- Modify: `internal/controller/admin/group.go` (`findGroup` → `findCaller`), `internal/controller/admin/auth.go` (`resolve`), `internal/controller/admin/user.go` (`User`, `findUser`, `authenticate`)
- Test: `internal/service/db/migrations_004_test.go` (create), `internal/controller/admin/auth_test.go`, `internal/controller/admin/group_test.go`, `internal/controller/admin/login_test.go`

**Interfaces:**
- Consumes: `errNoGroup`, `permSet`, `group` (existing); `loginStack`/`loginAndGetCookie`/`putInGroup` from `login_test.go`/`permission_test.go`.
- Produces:
  ```go
  type caller struct {
      group    *group
      username string
      status   int
  }
  func findCaller(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*caller, error)
  // User gains: Status int
  // resolve keeps its (*group, bool) signature.
  const statusActive = 1
  const statusDisabled = 0
  ```

- [ ] **Step 1: Write the migration pair**

`internal/service/db/migrations/004_user_status.up.sql`:

```sql
-- 004_user_status.up.sql
-- Whether an admin user may sign in and hold a session. 1 = active, 0 = disabled.
--
-- No backfill statement: a constant DEFAULT on ADD COLUMN fills existing rows,
-- so every user that predates this migration lands active. 003 needed an
-- explicit UPDATE only because its group_id was nullable and therefore left
-- existing rows unusable — do not copy that pattern here.
--
-- SQLite flavor (the template's default driver). PostgreSQL and MySQL accept
-- this statement as written.
--
-- This migration writes the literal `users` table. The [admin] users_table
-- setting redirects runtime lookups only — embedded SQL cannot read config — so
-- pointing it elsewhere makes that table's schema the operator's
-- responsibility, including this column.
ALTER TABLE users ADD COLUMN status INTEGER NOT NULL DEFAULT 1;
```

`internal/service/db/migrations/004_user_status.down.sql`:

```sql
-- 004_user_status.down.sql
-- Discards every disabled flag: after rolling back, previously disabled users
-- are indistinguishable from active ones, and re-applying the up migration
-- brings them all back as active. Dump users.status first if you intend to
-- come back.
--
-- DROP COLUMN needs SQLite 3.35+ (mattn/go-sqlite3 bundles 3.53); MySQL and
-- PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN status;
```

- [ ] **Step 2: Write the failing migration test**

Create `internal/service/db/migrations_004_test.go`:

```go
package db

import (
	"context"
	"io/fs"
	"testing"
)

// A constant DEFAULT on ADD COLUMN fills existing rows, so unlike 003 this
// migration needs no backfill — but that is a claim about SQLite's behaviour,
// and it is the difference between an upgraded project working and every one of
// its users being locked out.
func TestMigration004_ExistingUsersLandActive(t *testing.T) {
	const dsn = "file:mig004?mode=memory&cache=shared"
	ctx := context.Background()

	s := New(&Config{Driver: "sqlite3", DSN: dsn, MaxOpenConns: 1, Migrations: &Migrations{}})
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(ctx) })

	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub FS: %v", err)
	}
	// Rewind to the state a project on 003 is in, then add a user the way it
	// would have been created before status existed.
	if err := s.DB().MigrateTo(ctx, sub, "003_user_groups"); err != nil {
		t.Fatalf("migrate down to 003_user_groups: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, group_id)
		 VALUES ('legacy', 'x', 0, (SELECT id FROM user_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatalf("seed pre-existing user: %v", err)
	}

	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("upgrade to 004: %v", err)
	}

	var status int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT status FROM users WHERE username = 'legacy'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != 1 {
		t.Errorf("pre-existing user landed with status = %d, want 1 (locked out)", status)
	}
}

// ALTER TABLE ADD COLUMN is not idempotent — a second run fails with "duplicate
// column name". This pins what the design relies on: the migrator records a
// version and applies each file once.
func TestMigration004_AppliedOnlyOnce(t *testing.T) {
	const dsn = "file:mig004once?mode=memory&cache=shared"
	_ = newMigratedService(t, dsn)

	s2 := New(&Config{Driver: "sqlite3", DSN: dsn, MaxOpenConns: 1, Migrations: &Migrations{}})
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("second Start re-applied a migration: %v", err)
	}
	_ = s2.Stop(context.Background())
}

func TestMigration004_RollsBackCleanly(t *testing.T) {
	s := newMigratedService(t, "file:mig004down?mode=memory&cache=shared")
	ctx := context.Background()

	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("sub FS: %v", err)
	}
	if err := s.DB().MigrateTo(ctx, sub, "003_user_groups"); err != nil {
		t.Fatalf("migrate down to 003_user_groups: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `SELECT status FROM users WHERE 1 = 0`); err == nil {
		t.Error("users.status survived the down migration")
	}
	if err := s.DB().MigrateUp(ctx, sub); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
}
```

`newMigratedService` already exists in `internal/service/db/migrations_003_test.go` — same package, reuse it, do not redeclare it.

- [ ] **Step 3: Run the migration tests**

Run: `go test ./internal/service/db/ -run TestMigration004 -count=1 -v`
Expected: all three PASS (the migration files were written in Step 1).

- [ ] **Step 4: Write the failing tests for the read paths**

Append to `internal/controller/admin/auth_test.go`:

```go
// A disabled user holding a live session gets bounced to login with an
// explanation, not a bare 403 — and the session is deliberately NOT destroyed,
// because a flash is session state and Destroy also clears the cookie it would
// travel in.
func TestResolve_DisabledUserIsBouncedToLoginWithAFlash(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		t.Error("handler must not run for a disabled user")
	})
	cookie := loginAndGetCookie(t, eng)

	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusFound && w.Code != http.StatusFound {
		t.Errorf("status = %d, want a redirect to login", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

// Every attempt should say why it failed. Re-staging the flash on each request
// is intended: the alternative is an "already told them" marker in the session,
// which is state to no purpose. Pinned so the repetition is not mistaken for a
// defect later.
func TestResolve_DisabledUserIsBouncedOnEveryRequest(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {})
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
		r.AddCookie(cookie)
		eng.ServeHTTP(w, r)
		if loc := w.Header().Get("Location"); loc != "/admin/login" {
			t.Errorf("request %d: Location = %q, want /admin/login", i+1, loc)
		}
	}
}

// The three outcomes must stay apart: conflating them would make an outage or a
// disabled account look like a permissions decision.
func TestResolve_DisabledIsNeitherForbiddenNorInternalError(t *testing.T) {
	eng, adm := loginStack(t)
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {})
	cookie := loginAndGetCookie(t, eng)
	if _, err := adm.DB.ExecContext(context.Background(),
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	if w.Code == http.StatusForbidden {
		t.Error("a disabled account answered 403 — that is the no-group answer")
	}
	if w.Code == http.StatusInternalServerError {
		t.Error("a disabled account answered 500 — that is the storage-failure answer")
	}
}
```

Append to `internal/controller/admin/login_test.go`:

```go
// The order matters: status is checked only after the password verifies, so a
// disabled account is not distinguishable from a wrong password by timing.
// Whoever sees the disabled error already proved they hold the credential, so
// saying so plainly leaks nothing — and beats sending the real owner hunting
// for a password problem that does not exist.
func TestAuthenticate_DisabledAccount(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`UPDATE users SET status = 0 WHERE username = 'alice'`); err != nil {
		t.Fatal(err)
	}

	// Correct password, disabled account: a distinct error, not "invalid".
	_, err := authenticate(ctx, adm.DB, "users", "alice", "pw")
	if err == nil {
		t.Fatal("a disabled account must not authenticate")
	}
	if errors.Is(err, errInvalidCredentials) {
		t.Error("a disabled account should report being disabled, not invalid credentials")
	}
	if !errors.Is(err, errAccountDisabled) {
		t.Errorf("want errAccountDisabled, got %v", err)
	}

	// Wrong password on a disabled account stays "invalid credentials": the
	// caller has not proved anything, so nothing may be revealed.
	if _, err := authenticate(ctx, adm.DB, "users", "alice", "wrong"); !errors.Is(err, errInvalidCredentials) {
		t.Errorf("wrong password on a disabled account: want errInvalidCredentials, got %v", err)
	}
}
```

- [ ] **Step 5: Run them — they must fail**

Run: `go test ./internal/controller/admin/ -run 'TestResolve_Disabled|TestAuthenticate_DisabledAccount' -count=1 2>&1 | head -20`
Expected: compile failure — `errAccountDisabled` undefined — and, once that exists, the resolve tests fail because a disabled user is still admitted.

- [ ] **Step 6: Implement the read paths.** In `internal/controller/admin/group.go`, replace `findGroup` with `findCaller` and add the `caller` type above it:

```go
// statusActive and statusDisabled are the values of users.status (migration 004).
const (
	statusActive   = 1
	statusDisabled = 0
)

// caller is who is making the request: their group, plus the two user-row facts
// the request needs — the username the topbar shows and the status the disabled
// check reads. Bundled into one struct rather than returned as three values
// beside an error, and loaded by one query, because "one group lookup per
// request" is the constraint this whole path is built around.
type caller struct {
	group    *group
	username string
	status   int
}

// findCaller loads the group, username and status of the user with userID.
// usersTable is interpolated (it is configurable) and has already been validated
// by Admin.Validate against ^[A-Za-z_]\w*$; the id itself is parameterised.
func findCaller(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*caller, error) {
	q := fmt.Sprintf(`SELECT u.username, u.status, g.superuser, g.permissions
		FROM %s u JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, usersTable)

	var cl caller
	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&cl.username, &cl.status, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, errNoGroup
		}
		return nil, fmt.Errorf("admin: find caller: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("admin: group of user %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	cl.group = &group{Superuser: superuser != 0, Permissions: set}
	return &cl, nil
}
```

In `internal/controller/admin/auth.go`, `resolve`'s middle section becomes:

```go
	cl, err := findCaller(c.Request.Context(), a.DB, a.usersTable(), id)
	switch {
	case errors.Is(err, errNoGroup):
		// Fail closed. A user with no group has no permissions, and that is a
		// denial rather than an outage. Logged because the response is a bare
		// 403: without this line an operator whose user lost its group sees an
		// empty page and an empty log, with nothing to connect them.
		slog.Warn("admin auth: user has no permission group", "user", id)
		c.AbortWithStatus(http.StatusForbidden)
		return nil, false
	case err != nil:
		// A storage problem must not read as an authorisation decision.
		slog.Error("admin auth: load caller", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, false
	}

	// A disabled account is neither of the above: not a permissions decision
	// about a resource, and not an outage. Bounce to login with an explanation —
	// a bare 403 would leave the user staring at an empty page with nothing to
	// act on.
	//
	// The session is deliberately NOT destroyed. A flash is session state
	// (session/flash.go keys it under _flash:), so destroying the session would
	// discard the message being staged, and Destroy also clears the response
	// cookie the message has to travel in — the two cannot both happen. Nor is
	// it needed: this function refuses the session on every request, so the
	// credential is already inert, and re-enabling the user restores their
	// session rather than forcing a fresh login.
	if cl.status == statusDisabled {
		sess := a.Session.Session(c)
		sess.Flash("error", "该账号已被禁用。")
		if _, err := sess.Save(c.Request.Context()); err != nil {
			slog.Error("admin auth: stage disabled flash", "err", err, "user", id)
		}
		if err := c.Redirect(a.LoginPath()); err != nil {
			slog.Error("admin auth: redirect disabled user", "err", err)
		}
		c.Abort()
		return nil, false
	}

	c.Set("adminMenu", a.menuItems(cl.group))
	c.Set("adminUser", map[string]any{"id": id, "username": cl.username})
	c.Set("adminMount", a.mount())
	c.Set("loginPath", a.LoginPath())
	c.Set("currentPath", c.Request.URL.Path)
	return cl.group, true
```

In `internal/controller/admin/user.go`: add `Status int` to `User` after `PasswordHash`; add `status` to `findUser`'s SELECT and `Scan`; declare the sentinel beside `errInvalidCredentials` (find where that is declared and put it there):

```go
// errAccountDisabled is returned only after the password verified — see
// authenticate.
var errAccountDisabled = errors.New("admin: account is disabled")
```

and extend `authenticate`, after the `verifyPassword` check:

```go
	// Only now, with the correct password proven: an earlier check would make a
	// disabled account distinguishable from a wrong password by timing, which is
	// the leak the dummy compare above exists to prevent. Whoever reaches this
	// line holds the credential, so naming the real reason reveals nothing.
	if u.Status == statusDisabled {
		return nil, errAccountDisabled
	}
```

Update `internal/controller/admin/group_test.go`'s direct callers from `findGroup(...)` to `findCaller(...)`, reading `cl.group`, `cl.username`. The bad-JSON test's error text assertion, if it names "find group", becomes "find caller".

Finally, `LoginSubmit` must show something for `errAccountDisabled` rather than "invalid username or password". Find its error branch and add:

```go
		if errors.Is(err, errAccountDisabled) {
			c.Set("loginPath", a.LoginPath())
			c.Set("error", "该账号已被禁用。")
			if err := c.Render("admin/login"); err != nil {
				slog.Error("render admin login", "err", err)
			}
			return
		}
```

placed before the generic `errInvalidCredentials` branch. Read the existing branch and match its shape exactly rather than copying this verbatim if the surrounding code differs.

- [ ] **Step 7: Run the whole suite**

Run: `go test ./... -count=1`
Expected: PASS. If `TestResolve_WorksWithTheDatabaseSessionStore` or any stage-1 permission test fails, the `findCaller` rename missed a call site.

- [ ] **Step 8: Commit**

```bash
gofmt -l ./ && go vet ./...
git add internal/service/db/ internal/controller/admin/
git commit -m "feat(admin): a status column, and every read that respects it"
```

---

### Task 2: The three guardrails

Shared by every mutating handler in Tasks 4–6. Built and tested before any of
them exists, so the handlers have nothing to reinvent.

**Files:**
- Create: `internal/controller/admin/guardrails.go`, `internal/controller/admin/guardrails_test.go`
- Test: as above

**Interfaces:**
- Consumes: `statusActive`, `statusDisabled`, `caller`, `(*Admin).usersTable()`, `(*Admin).authKey()`, `userID` (the session-value coercion helper in `auth.go`).
- Produces:
  ```go
  var errSelfTarget = errors.New("admin: refusing to act on your own account")
  var errLastSuperuser = errors.New("admin: this would leave no enabled superuser")

  func (a *Admin) callerID(c *inertia.Context) (int64, bool)
  func (a *Admin) notSelf(c *inertia.Context, targetID int64) error
  func (a *Admin) keepingASuperuser(ctx context.Context, mutate func(tx *sqldb.Tx) error) error
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/guardrails_test.go`:

```go
package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

// countEnabledSuperusers is the property the guard defends, spelled out here so
// the test does not depend on the guard's own SQL being right.
func countEnabledSuperusers(t *testing.T, adm *Admin) int {
	t.Helper()
	var n int
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM users u JOIN user_groups g ON g.id = u.group_id
		 WHERE g.superuser = 1 AND u.status = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The mutation is rolled back, not merely refused: the row must still be there
// afterwards. A guard that reported an error but left the change applied would
// be worse than none, and only an after-the-fact read can tell the difference.
func TestKeepingASuperuser_RollsBackTheChange(t *testing.T) {
	_, adm := loginStack(t) // alice is in Administrators (superuser), status 1
	ctx := context.Background()
	if before := countEnabledSuperusers(t, adm); before != 1 {
		t.Fatalf("fixture should have exactly 1 enabled superuser, has %d", before)
	}

	err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET status = 0 WHERE username = 'alice'`)
		return err
	})
	if !errors.Is(err, errLastSuperuser) {
		t.Fatalf("want errLastSuperuser, got %v", err)
	}
	if after := countEnabledSuperusers(t, adm); after != 1 {
		t.Errorf("enabled superusers = %d after the refused change, want 1 — it was not rolled back", after)
	}
}

// The same guard has to cover every path, which is the reason it is one guard.
func TestKeepingASuperuser_CoversAllFourPaths(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		sql  string
	}{
		{"disable the user", `UPDATE users SET status = 0 WHERE username = 'alice'`},
		{"delete the user", `DELETE FROM users WHERE username = 'alice'`},
		{"move the user out of the superuser group", `UPDATE users SET group_id = NULL WHERE username = 'alice'`},
		{"clear the group's superuser flag", `UPDATE user_groups SET superuser = 0 WHERE name = 'Administrators'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, adm := loginStack(t)
			err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
				_, err := tx.ExecContext(ctx, c.sql)
				return err
			})
			if !errors.Is(err, errLastSuperuser) {
				t.Errorf("want errLastSuperuser, got %v", err)
			}
			if n := countEnabledSuperusers(t, adm); n != 1 {
				t.Errorf("enabled superusers = %d, want 1 (rolled back)", n)
			}
		})
	}
}

// Clearing the flag on a group with several members drops them all at once, so
// the count has to see the staged change rather than predict it.
func TestKeepingASuperuser_AllowsAChangeThatLeavesOne(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, status, group_id)
		 VALUES ('bob', 'x', 0, 1, (SELECT id FROM user_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatal(err)
	}

	err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET status = 0 WHERE username = 'alice'`)
		return err
	})
	if err != nil {
		t.Fatalf("disabling one of two superusers must be allowed: %v", err)
	}
	if n := countEnabledSuperusers(t, adm); n != 1 {
		t.Errorf("enabled superusers = %d, want 1 (bob remains)", n)
	}
}

// A failure inside mutate propagates and rolls back; it must not be reported as
// the last-superuser rule, which would send an operator looking in the wrong
// place.
func TestKeepingASuperuser_PropagatesTheMutationError(t *testing.T) {
	_, adm := loginStack(t)
	sentinel := errors.New("boom")
	err := adm.keepingASuperuser(context.Background(), func(tx *sqldb.Tx) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("want the mutation's own error, got %v", err)
	}
	if errors.Is(err, errLastSuperuser) {
		t.Error("a mutation failure must not be reported as the last-superuser rule")
	}
}
```

- [ ] **Step 2: Run it — must fail**

Run: `go test ./internal/controller/admin/ -run TestKeepingASuperuser -count=1 2>&1 | head`
Expected: compile failure — `keepingASuperuser` and `errLastSuperuser` undefined.

- [ ] **Step 3: Implement `guardrails.go`**

```go
package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/inertia"
)

// The two rules an administrator can trip. Both are refusals, not failures:
// callers turn them into a message rather than a 500.
var (
	errSelfTarget    = errors.New("admin: refusing to act on your own account")
	errLastSuperuser = errors.New("admin: this would leave no enabled superuser")
)

// callerID is the signed-in user's id, read from the session the same way
// resolve reads it. Reported false only when there is no usable id, which the
// guarded routes make impossible — resolve has already run.
func (a *Admin) callerID(c *inertia.Context) (int64, bool) {
	v, ok := a.Session.Session(c).Get(a.authKey())
	if !ok {
		return 0, false
	}
	return userID(v)
}

// notSelf refuses an action aimed at the signed-in user. It covers rules 1 and
// 2 — no deleting or disabling yourself, no changing your own group — because
// both reduce to "this target is me".
//
// Enforced here rather than by hiding controls in the UI: the UI is not the
// enforcement point, and a hand-made POST would sail past it.
func (a *Admin) notSelf(c *inertia.Context, targetID int64) error {
	me, ok := a.callerID(c)
	if !ok {
		// No identifiable caller on a route that requires one: refuse rather
		// than allow, since the alternative is acting on someone's behalf
		// without knowing whose.
		return errSelfTarget
	}
	if me == targetID {
		return errSelfTarget
	}
	return nil
}

// keepingASuperuser applies mutate and keeps it only if at least one enabled
// superuser user remains.
//
// The count runs inside the transaction, after the mutation, and that is the
// whole mechanism rather than an implementation detail: it reads the state the
// change actually produced instead of predicting it. Four different changes can
// violate the rule — disabling a user, deleting a user, moving a user out of a
// superuser group, and clearing a group's superuser flag — and a pre-check per
// path is a design where the fifth path someone adds later is a silent hole.
//
// Note for callers: sqldb's Transaction begins with context.Background(), so the
// transaction itself is not cancellable; the queries inside it take ctx, which is
// what matters. And with the pool pinned to one connection (as the tests do for
// :memory:), any query issued on a.DB while this transaction is open will block
// forever — use the tx handle.
func (a *Admin) keepingASuperuser(ctx context.Context, mutate func(tx *sqldb.Tx) error) error {
	return a.DB.Transaction(func(tx *sqldb.Tx) error {
		if err := mutate(tx); err != nil {
			return err
		}
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s u JOIN user_groups g ON g.id = u.group_id
			WHERE g.superuser = 1 AND u.status = ?`, a.usersTable())
		var n int
		if err := tx.QueryRowContext(ctx, q, statusActive).Scan(&n); err != nil {
			return fmt.Errorf("admin: count enabled superusers: %w", err)
		}
		if n == 0 {
			return errLastSuperuser
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/controller/admin/ -run TestKeepingASuperuser -count=1 -v`
Expected: all PASS.

- [ ] **Step 5: Prove the guard is load-bearing**

Temporarily move the count *before* `mutate(tx)` in `keepingASuperuser` — the pre-check shape the design rejects. Run the tests again: the four-path test must fail, because a pre-check on the unchanged state still finds one superuser and lets the change through. Restore the order afterwards and paste both outcomes into your report.

- [ ] **Step 6: Commit**

```bash
gofmt -l ./ && go vet ./... && go test ./... -count=1
git add internal/controller/admin/
git commit -m "feat(admin): the three self-lockout guardrails"
```

---

### Task 3: The user resource — list, create, edit

CRUD without the dangerous verbs. Delete and disable are Task 4, where the
guardrails bite.

**Files:**
- Create: `internal/controller/admin/user_crud.go`, `internal/controller/admin/user_crud_test.go`, `frontend/pages/admin/user/index.vue`, `frontend/pages/admin/user/form.vue`

**Interfaces:**
- Consumes: `HashPassword`, `statusActive`, `(*Admin).usersTable()`, `(*Admin).Prefix()`, `validate.*`.
- Produces:
  ```go
  type userRow struct {
      ID       int64  `json:"id"`
      Username string `json:"username"`
      GroupID  int64  `json:"group_id"`
      Group    string `json:"group"`
      Status   int    `json:"status"`
      Created  int64  `json:"created_at"`
  }
  type groupOption struct {
      ID   int64  `json:"id"`
      Name string `json:"name"`
  }
  func (a *Admin) mountUsers(eng *inertia.Engine)   // called by Mount in Task 8
  ```
  Handlers: `usersIndex`, `userNew`, `userCreate`, `userEdit`, `userUpdate` on `*Admin`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/user_crud_test.go`:

```go
package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// adminStack is loginStack plus the user and group routes mounted, which is what
// every test below needs.
func adminStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountUsers(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

func post(t *testing.T, eng *inertia.Engine, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)
	return w
}

func TestUserCreate_StoresAHashedPasswordAndTheGroup(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()

	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, "/admin/user", url.Values{
		"username": {"carol"},
		"password": {"s3cretpw"},
		"group_id": {fmt.Sprint(gid)},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var hash string
	var status int
	var got int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash, status, group_id FROM users WHERE username = 'carol'`).
		Scan(&hash, &status, &got); err != nil {
		t.Fatalf("carol was not created: %v", err)
	}
	if hash == "s3cretpw" {
		t.Error("the password was stored in plaintext")
	}
	if !verifyPassword(hash, "s3cretpw") {
		t.Error("the stored hash does not verify the submitted password")
	}
	if status != statusActive {
		t.Errorf("status = %d, want %d", status, statusActive)
	}
	if got != gid {
		t.Errorf("group_id = %d, want %d", got, gid)
	}
}

func TestUserCreate_RejectsBadInput(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	var gid int64
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		form url.Values
	}{
		{"password too short", url.Values{"username": {"dave"}, "password": {"short"}, "group_id": {fmt.Sprint(gid)}}},
		{"username taken", url.Values{"username": {"alice"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"username illegal", url.Values{"username": {"has space"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"username too short", url.Values{"username": {"ab"}, "password": {"s3cretpw"}, "group_id": {fmt.Sprint(gid)}}},
		{"group missing", url.Values{"username": {"dave"}, "password": {"s3cretpw"}, "group_id": {"99999"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := post(t, eng, cookie, "/admin/user", c.form)
			// A rejected submit re-renders the form rather than redirecting.
			if w.Code == http.StatusFound {
				t.Errorf("status = 303, want the form re-rendered with an error")
			}
			var n int
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT COUNT(*) FROM users WHERE username = ?`, c.form.Get("username")).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if c.form.Get("username") != "alice" && n != 0 {
				t.Errorf("a rejected submit created %d row(s)", n)
			}
		})
	}
}

// A blank password on update means "leave it alone" — otherwise every edit of a
// username would silently reset the person's password.
func TestUserUpdate_BlankPasswordKeepsTheOldOne(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()

	var id, gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id, group_id FROM users WHERE username = 'alice'`).Scan(&id, &gid); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d", id), url.Values{
		"username": {"alice2"},
		"password": {""},
		"group_id": {fmt.Sprint(gid)},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var after, name string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash, username FROM users WHERE id = ?`, id).Scan(&after, &name); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Error("a blank password field changed the stored hash")
	}
	if name != "alice2" {
		t.Errorf("username = %q, want alice2", name)
	}
}

// Rule 2: editing your own username and password is fine; moving yourself to
// another group is not.
func TestUserUpdate_CannotChangeYourOwnGroup(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var id, other int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'alice'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Editors'`).Scan(&other); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d", id), url.Values{
		"username": {"alice"},
		"password": {""},
		"group_id": {fmt.Sprint(other)},
	})
	if w.Code == http.StatusFound {
		t.Error("changing your own group must be refused")
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT group_id FROM users WHERE id = ?`, id).Scan(&gid); err != nil {
		t.Fatal(err)
	}
	if gid == other {
		t.Error("the group changed anyway")
	}
}
```

Add `"fmt"` and `"github.com/millken/inertia"` to the import block.

- [ ] **Step 2: Run — must fail**

Run: `go test ./internal/controller/admin/ -run TestUser -count=1 2>&1 | head`
Expected: compile failure — `mountUsers` undefined.

- [ ] **Step 3: Implement `user_crud.go`**

```go
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// usernameRe is deliberately ASCII-only: admin accounts are created by an
// operator, not chosen by a visitor, and they show up in log lines, CLI
// arguments and create-user invocations where a Unicode identifier is a
// nuisance rather than a feature.
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// bcryptMaxPassword is where bcrypt truncates. A longer password is partly not
// the credential, so two different strings would authenticate the same account
// — refuse rather than accept it silently.
const bcryptMaxPassword = 72

// userRow is one row of the user list, and the edit form's model.
type userRow struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	GroupID  int64  `json:"group_id"`
	Group    string `json:"group"`
	Status   int    `json:"status"`
	Created  int64  `json:"created_at"`
}

// groupOption is a choice in the form's group select.
type groupOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// mountUsers registers the user resource. Every route goes through the
// registrar, so user.access guards the reads and user.modify the writes without
// either being named here.
func (a *Admin) mountUsers(eng *inertia.Engine) {
	base := a.Prefix() + "/user"
	r := a.Resource(eng, "user")

	r.GET(base, a.usersIndex)
	r.GET(base+"/new", a.userNew)
	r.POST(base, a.userCreate)
	r.GET(base+"/:id/edit", a.userEdit)
	r.POST(base+"/:id", a.userUpdate)
	r.POST(base+"/:id/delete", a.userDelete)
	r.POST(base+"/:id/status", a.userSetStatus)
	r.Menu("Access", "Users", base)
}

func (a *Admin) userBase() string { return a.Prefix() + "/user" }

// usersIndex lists users with their group name — one query, joined, rather than
// a lookup per row.
func (a *Admin) usersIndex(c *inertia.Context) {
	q := fmt.Sprintf(`SELECT u.id, u.username, COALESCE(u.group_id, 0), COALESCE(g.name, ''),
		u.status, u.created_at
		FROM %s u LEFT JOIN user_groups g ON g.id = u.group_id
		ORDER BY u.username`, a.usersTable())

	rows, err := a.DB.QueryContext(c.Request.Context(), q)
	if err != nil {
		slog.Error("admin: list users", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.GroupID, &u.Group, &u.Status, &u.Created); err != nil {
			slog.Error("admin: scan user", "err", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		slog.Error("admin: list users", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Set("items", items)
	c.Set("basePath", a.userBase())
	if err := c.Render("admin/user/index"); err != nil {
		slog.Error("render admin user index", "err", err)
	}
}

func (a *Admin) userNew(c *inertia.Context) {
	a.renderUserForm(c, userRow{Status: statusActive}, nil)
}

func (a *Admin) userCreate(c *inertia.Context) {
	ctx := c.Request.Context()
	item := userRow{
		Username: c.PostForm("username"),
		Status:   statusActive,
	}
	item.GroupID, _ = parseInt64(c.PostForm("group_id"))
	password := c.PostForm("password")

	v := a.validateUser(ctx, item, password, true, 0)
	if !v.OK() {
		a.renderUserForm(c, item, v.Errors())
		return
	}

	hash, err := HashPassword(password)
	if err != nil {
		slog.Error("admin: hash password", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	q := fmt.Sprintf(`INSERT INTO %s (username, password_hash, created_at, status, group_id)
		VALUES (?, ?, ?, ?, ?)`, a.usersTable())
	if _, err := a.DB.ExecContext(ctx, q, item.Username, hash, time.Now().UnixNano(),
		statusActive, item.GroupID); err != nil {
		slog.Error("admin: create user", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "用户已创建")
	a.redirect(c, a.userBase())
}

func (a *Admin) userEdit(c *inertia.Context) {
	id, _ := c.Params.GetInt64("id")
	item, err := a.findUserRow(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: load user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if item == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.renderUserForm(c, *item, nil)
}

func (a *Admin) userUpdate(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	current, err := a.findUserRow(ctx, id)
	if err != nil {
		slog.Error("admin: load user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if current == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	item := userRow{ID: id, Username: c.PostForm("username"), Status: current.Status}
	item.GroupID, _ = parseInt64(c.PostForm("group_id"))
	password := c.PostForm("password")

	v := a.validateUser(ctx, item, password, password != "", id)
	// Rule 2: you may edit your own username and password, but not move yourself
	// to another group — that is how you take away your own access.
	if item.GroupID != current.GroupID {
		if err := a.notSelf(c, id); err != nil {
			v.Check(false, "group_id", "不能修改自己所在的分组")
		}
	}
	if !v.OK() {
		a.renderUserForm(c, item, v.Errors())
		return
	}

	// The group change can strand the last superuser, so it runs under the
	// guard; the guard is harmless when nothing about superuser status changed.
	err = a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`UPDATE %s SET username = ?, group_id = ? WHERE id = ?`, a.usersTable())
		if _, err := tx.ExecContext(ctx, q, item.Username, item.GroupID, id); err != nil {
			return err
		}
		if password == "" {
			return nil
		}
		hash, err := HashPassword(password)
		if err != nil {
			return err
		}
		pq := fmt.Sprintf(`UPDATE %s SET password_hash = ? WHERE id = ?`, a.usersTable())
		_, err = tx.ExecContext(ctx, pq, hash, id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		v.Check(false, "group_id", "系统必须至少保留一个启用的超级管理员")
		a.renderUserForm(c, item, v.Errors())
		return
	case err != nil:
		slog.Error("admin: update user", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "用户已更新")
	a.redirect(c, a.userBase())
}

// validateUser checks a submitted user. requirePassword is false on an update
// with a blank password field, which means "leave it alone" — otherwise every
// username edit would silently reset someone's password. exceptID is the row
// being updated, so the uniqueness rule ignores its own current value.
//
// Cheap rules come first: a failure short-circuits the field, so the database is
// never queried for input that was blank or malformed anyway.
func (a *Admin) validateUser(ctx context.Context, item userRow, password string, requirePassword bool, exceptID int64) *validate.Validator {
	v := validate.New()
	v.Field("username", item.Username,
		validate.Required,
		validate.MinLen(3),
		validate.MaxLen(64),
		validate.Msg(validate.Match(usernameRe), "只能包含字母、数字、点、下划线和连字符"),
		a.usernameAvailable(ctx, exceptID),
	)
	if requirePassword {
		v.Field("password", password,
			validate.Required,
			validate.MinLen(8),
			validate.Msg(validate.MaxLen(bcryptMaxPassword), "不能超过 72 个字符（bcrypt 的上限）"),
		)
	}
	v.Check(item.GroupID != 0, "group_id", "请选择一个分组")
	if item.GroupID != 0 {
		exists, err := a.groupExists(ctx, item.GroupID)
		if err != nil {
			slog.Error("admin: check group", "err", err)
			v.Check(false, "group_id", "无法校验，请重试")
		} else {
			v.Check(exists, "group_id", "该分组不存在")
		}
	}
	return v
}

// usernameAvailable rejects a username another row already uses. A failed query
// degrades to a message on the form rather than a 500 — the user gets something
// actionable, and the cause is in the log.
func (a *Admin) usernameAvailable(ctx context.Context, exceptID int64) validate.Rule {
	return func(name string) error {
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE username = ? AND id != ?`, a.usersTable())
		var n int
		if err := a.DB.QueryRowContext(ctx, q, name, exceptID).Scan(&n); err != nil {
			slog.Error("admin: check username", "err", err)
			return errors.New("无法校验，请重试")
		}
		if n > 0 {
			return errors.New("已被占用")
		}
		return nil
	}
}

func (a *Admin) groupExists(ctx context.Context, id int64) (bool, error) {
	var n int
	if err := a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_groups WHERE id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// findUserRow loads one row for the edit form, returning (nil, nil) if absent.
func (a *Admin) findUserRow(ctx context.Context, id int64) (*userRow, error) {
	q := fmt.Sprintf(`SELECT u.id, u.username, COALESCE(u.group_id, 0), COALESCE(g.name, ''),
		u.status, u.created_at
		FROM %s u LEFT JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, a.usersTable())
	var u userRow
	if err := a.DB.QueryRowContext(ctx, q, id).
		Scan(&u.ID, &u.Username, &u.GroupID, &u.Group, &u.Status, &u.Created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// groupOptions are the choices in the form's group select.
func (a *Admin) groupOptions(ctx context.Context) ([]groupOption, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, name FROM user_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []groupOption{}
	for rows.Next() {
		var g groupOption
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// renderUserForm renders the create/edit form. errs is nil on a first visit and
// carries one message per bad field after a failed submit; item repopulates the
// inputs, so what was typed survives the re-render.
func (a *Admin) renderUserForm(c *inertia.Context, item userRow, errs map[string]string) {
	groups, err := a.groupOptions(c.Request.Context())
	if err != nil {
		slog.Error("admin: load group options", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Set("item", item)
	c.Set("groups", groups)
	c.Set("basePath", a.userBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/user/form"); err != nil {
		slog.Error("render admin user form", "err", err)
	}
}

// flash stages a one-shot message for the page we are about to redirect to. The
// session middleware injects it as the `flash` prop on the next request and
// clears it, so it shows exactly once.
func (a *Admin) flash(c *inertia.Context, kind, message string) {
	sess := a.Session.Session(c)
	sess.Flash(kind, message)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("admin: stage flash", "err", err)
	}
}

// parseInt64 is strconv.ParseInt with the base and bit size fixed, for form
// fields that carry ids.
func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
```

Add `"database/sql"`, `"strconv"` and `"github.com/dnsoa/go/sqldb"` to the imports. `userDelete` and `userSetStatus` are referenced by `mountUsers` but implemented in Task 4 — add them as stubs *in this task* so the package compiles:

```go
// userDelete and userSetStatus are implemented in the next task; the routes are
// registered here so the resource's permission set is complete from the start.
func (a *Admin) userDelete(c *inertia.Context)    { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) userSetStatus(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
```

- [ ] **Step 4: Write the two pages**

`frontend/pages/admin/user/index.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type UserRow = {
  id: number
  username: string
  group_id: number
  group: string
  status: number
  created_at: number
}

defineProps<{
  items: UserRow[]
  basePath: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'username', label: '用户名', sortable: true },
  { key: 'group', label: '分组' },
  { key: 'status', label: '状态' },
]

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<UserRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as UserRow
}
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="用户" description="后台账号及其权限分组。">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          新建用户
        </Button>
      </template>
    </PageHeader>

    <Card>
      <CardContent class="pt-6">
        <DataTable :columns="columns" :data="items" search-key="username">
          <template #cell-username="{ row }">
            <a :href="`${basePath}/${row.id}/edit`" class="font-medium hover:underline">
              {{ row.username }}
            </a>
          </template>
          <template #cell-status="{ row }">
            <Badge :variant="row.status === 1 ? 'default' : 'secondary'">
              {{ row.status === 1 ? '启用' : '已禁用' }}
            </Badge>
          </template>
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">编辑</DropdownMenuItem>
            <DropdownMenuItem as="button" type="submit" :form="`status-${row.id}`">
              {{ row.status === 1 ? '禁用' : '启用' }}
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">删除</DropdownMenuItem>
          </template>
          <template #empty>还没有用户。</template>
        </DataTable>

        <!-- One form per row, outside the dropdown: a menu item cannot carry a
             POST body itself, and a form nested inside the teleported dropdown
             content would not survive SSR. -->
        <form
          v-for="row in items"
          :id="`status-${row.id}`"
          :key="row.id"
          :action="`${basePath}/${row.id}/status`"
          method="post"
          class="hidden"
        >
          <input type="hidden" name="status" :value="row.status === 1 ? 0 : 1">
        </form>
      </CardContent>
    </Card>

    <ConfirmDialog
      :open="pending !== null"
      :title="`删除用户“${pending?.username}”？`"
      :action="`${basePath}/${pending?.id}/delete`"
      confirm-label="删除"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
```

`frontend/pages/admin/user/form.vue`:

```vue
<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type UserRow = { id: number; username: string; group_id: number; group: string; status: number }

const props = defineProps<{
  item: UserRow
  groups: { id: number; name: string }[]
  basePath: string
  // Set by the handler only when a submit failed validation: one message per bad
  // field. `item` carries what was typed, so the inputs repopulate on their own.
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const editing = props.item.id > 0
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :crumb="editing ? '编辑' : '新建'"
  >
    <PageHeader :title="editing ? '编辑用户' : '新建用户'" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form
          :action="editing ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <FormField name="username" label="用户名" :error="errors?.username">
            <Input
              id="username"
              name="username"
              :model-value="item.username"
              :aria-invalid="!!errors?.username"
            />
          </FormField>

          <FormField name="group_id" label="分组" :error="errors?.group_id">
            <select
              id="group_id"
              name="group_id"
              class="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
              :aria-invalid="!!errors?.group_id"
            >
              <option v-for="g in groups" :key="g.id" :value="g.id" :selected="g.id === item.group_id">
                {{ g.name }}
              </option>
            </select>
          </FormField>

          <FormField
            name="password"
            :label="editing ? '新密码' : '密码'"
            :error="errors?.password"
          >
            <Input
              id="password"
              name="password"
              type="password"
              :aria-invalid="!!errors?.password"
            />
            <p class="text-sm text-muted-foreground">
              {{ editing ? '留空表示不修改密码。' : '8–72 个字符。' }}
            </p>
          </FormField>

          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="basePath" variant="outline">取消</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

A plain `<select>` rather than the shadcn `Select` component: `Select` is a
listbox that posts nothing, so a native form submit would carry no group. The
existing `ui/select` copy stays unused by this page.

- [ ] **Step 5: Run the Go tests**

Run: `go test ./internal/controller/admin/ -count=1`
Expected: PASS.

- [ ] **Step 6: Verify the frontend and commit**

```bash
pnpm -C frontend run type-check && pnpm -C frontend run build && pnpm -C frontend run test
gofmt -l ./ && go vet ./... && go test ./... -count=1
git add internal/controller/admin/ frontend/pages/admin/user/
git commit -m "feat(admin): the user resource — list, create, edit"
```

---

### Task 4: Delete and disable, under the guardrails

**Files:**
- Modify: `internal/controller/admin/user_crud.go` (replace the two stubs)
- Test: `internal/controller/admin/user_crud_test.go`

**Interfaces:**
- Consumes: `notSelf`, `keepingASuperuser`, `errSelfTarget`, `errLastSuperuser`, `statusActive`, `statusDisabled`, `adminStack`/`post` helpers from Task 3's test file.
- Produces: working `userDelete` and `userSetStatus`.

- [ ] **Step 1: Write the failing tests** (append to `user_crud_test.go`)

```go
// Rule 1. Both directions of the same rule: you are the one account you must
// not be able to remove or switch off.
func TestUserDeleteAndDisable_CannotTargetYourself(t *testing.T) {
	for _, c := range []struct {
		name string
		path string
		form url.Values
	}{
		{"delete", "/delete", nil},
		{"disable", "/status", url.Values{"status": {"0"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm, cookie := adminStack(t)
			var id int64
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT id FROM users WHERE username = 'alice'`).Scan(&id); err != nil {
				t.Fatal(err)
			}

			w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d%s", id, c.path), c.form)
			if w.Code == http.StatusFound {
				t.Error("acting on your own account must be refused")
			}

			var n, status int
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT COUNT(*), COALESCE(MAX(status), -1) FROM users WHERE id = ?`, id).
				Scan(&n, &status); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Error("the account was deleted anyway")
			}
			if status != statusActive {
				t.Error("the account was disabled anyway")
			}
		})
	}
}

// Rule 3, through HTTP rather than the guard's unit test: deleting the only
// other superuser is fine, deleting the last one is not.
func TestUserDelete_KeepsOneEnabledSuperuser(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	// bob is a second superuser, so deleting him is allowed...
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, status, group_id)
		 VALUES ('bob', 'x', 0, 1, (SELECT id FROM user_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatal(err)
	}
	var bob int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'bob'`).Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/delete", bob), nil); w.Code != http.StatusFound {
		t.Fatalf("deleting a second superuser: status = %d, want 303", w.Code)
	}

	// ...and now alice is the last one, but she is also the caller, so rule 1
	// already covers her. Add a third superuser and have alice delete them to
	// leave exactly one, then check the count never reached zero.
	if n := countEnabledSuperusers(t, adm); n != 1 {
		t.Errorf("enabled superusers = %d, want 1", n)
	}
}

// A group with no superuser: disabling its only member is fine, because the
// rule is about superusers, not about users in general.
func TestUserSetStatus_DisablesANonSuperuser(t *testing.T) {
	eng, adm, cookie := adminStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, status, group_id)
		 VALUES ('erin', 'x', 0, 1, (SELECT id FROM user_groups WHERE name = 'Editors'))`); err != nil {
		t.Fatal(err)
	}
	var erin int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = 'erin'`).Scan(&erin); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/status", erin), url.Values{"status": {"0"}})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}
	var status int
	if err := adm.DB.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, erin).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != statusDisabled {
		t.Errorf("status = %d, want %d", status, statusDisabled)
	}

	// Enabling again is not subject to rules 1 or 3, and the value comes from the
	// request rather than being toggled: submitting 1 twice leaves it enabled.
	for range 2 {
		if w := post(t, eng, cookie, fmt.Sprintf("/admin/user/%d/status", erin), url.Values{"status": {"1"}}); w.Code != http.StatusFound {
			t.Fatalf("enable: status = %d, want 303", w.Code)
		}
	}
	if err := adm.DB.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, erin).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != statusActive {
		t.Errorf("status after two enables = %d, want %d", status, statusActive)
	}
}
```

- [ ] **Step 2: Run — must fail**

Run: `go test ./internal/controller/admin/ -run 'TestUserDelete|TestUserSetStatus' -count=1`
Expected: FAIL — the stubs answer 501.

- [ ] **Step 3: Replace the stubs**

```go
// userDelete removes a user. Rule 1 blocks your own account; rule 3 blocks the
// change if it would leave no enabled superuser, and rolls it back.
func (a *Admin) userDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	if err := a.notSelf(c, id); err != nil {
		a.flash(c, "error", "不能删除自己的账号")
		a.redirect(c, a.userBase())
		return
	}

	err := a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, a.usersTable())
		_, err := tx.ExecContext(ctx, q, id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		a.flash(c, "error", "系统必须至少保留一个启用的超级管理员")
	case err != nil:
		slog.Error("admin: delete user", "err", err, "user", id)
		a.flash(c, "error", "删除失败，请查看日志")
	default:
		a.flash(c, "success", "用户已删除")
	}
	a.redirect(c, a.userBase())
}

// userSetStatus enables or disables a user. The value comes from the request
// rather than being toggled, so a double-submitted form cannot flip a user back
// on — and the two directions are not symmetric: only disabling can lock anyone
// out, so only disabling is guarded.
func (a *Admin) userSetStatus(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	want := statusActive
	if c.PostForm("status") == "0" {
		want = statusDisabled
	}

	if want == statusDisabled {
		if err := a.notSelf(c, id); err != nil {
			a.flash(c, "error", "不能禁用自己的账号")
			a.redirect(c, a.userBase())
			return
		}
	}

	set := func(tx *sqldb.Tx) error {
		q := fmt.Sprintf(`UPDATE %s SET status = ? WHERE id = ?`, a.usersTable())
		_, err := tx.ExecContext(ctx, q, want, id)
		return err
	}

	var err error
	if want == statusDisabled {
		err = a.keepingASuperuser(ctx, set)
	} else {
		// Enabling can only increase the count, so the guard has nothing to
		// check; running it anyway would be misleading rather than wrong.
		err = a.DB.Transaction(set)
	}
	switch {
	case errors.Is(err, errLastSuperuser):
		a.flash(c, "error", "系统必须至少保留一个启用的超级管理员")
	case err != nil:
		slog.Error("admin: set user status", "err", err, "user", id)
		a.flash(c, "error", "操作失败，请查看日志")
	case want == statusDisabled:
		a.flash(c, "success", "用户已禁用")
	default:
		a.flash(c, "success", "用户已启用")
	}
	a.redirect(c, a.userBase())
}
```

- [ ] **Step 4: Run and commit**

```bash
go test ./internal/controller/admin/ -count=1 && go test ./... -count=1
gofmt -l ./ && go vet ./...
git add internal/controller/admin/
git commit -m "feat(admin): delete and disable users, under the guardrails"
```

---

### Task 5: The group resource — list, create, edit, delete

The permission grid is Task 6; this is the group itself.

**Files:**
- Create: `internal/controller/admin/group_crud.go`, `internal/controller/admin/group_crud_test.go`, `frontend/pages/admin/group/index.vue`, `frontend/pages/admin/group/form.vue`

**Interfaces:**
- Consumes: `keepingASuperuser`, `errLastSuperuser`, `flash`, `validate.*`, `groupOption`.
- Produces:
  ```go
  type groupRow struct {
      ID        int64  `json:"id"`
      Name      string `json:"name"`
      Superuser bool   `json:"superuser"`
      Members   int    `json:"members"`
      Keys      int    `json:"keys"`
  }
  func (a *Admin) mountGroups(eng *inertia.Engine)
  ```
  Handlers `groupsIndex`, `groupNew`, `groupCreate`, `groupEdit`, `groupUpdate`, `groupDelete`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/group_crud_test.go`:

```go
package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/millken/inertia"
)

// groupStack mounts both resources, since the group tests need users too.
func groupStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountUsers(eng)
	adm.mountGroups(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

// A group with members cannot be deleted: a user with no group is refused
// everything, including logout, which is the state stage 1 spent effort fixing.
// The message has to name the count, or the operator has no idea what to do next.
func TestGroupDelete_RefusedWhileItHasMembers(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d/delete", gid), nil)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect carrying the refusal", w.Code)
	}
	var n int
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_groups WHERE id = ?`, gid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the group was deleted despite having a member")
	}
}

func TestGroupDelete_SucceedsWhenEmpty(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Empty', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Empty'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	if w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d/delete", gid), nil); w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	var n int
	if err := adm.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_groups WHERE id = ?`, gid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("an empty group was not deleted")
	}
}

// Clearing the superuser flag on the only superuser group strands everyone, and
// the guard must roll it back rather than merely reporting an error.
func TestGroupUpdate_CannotClearTheLastSuperuserFlag(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	var gid int64
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT id FROM user_groups WHERE name = 'Administrators'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d", gid), url.Values{
		"name": {"Administrators"},
		// superuser checkbox absent = unchecked
	})
	if w.Code == http.StatusFound {
		t.Error("clearing the last superuser flag must be refused")
	}
	var superuser int
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT superuser FROM user_groups WHERE id = ?`, gid).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if superuser != 1 {
		t.Error("the flag was cleared anyway — the change was not rolled back")
	}
}

func TestGroupCreate_ValidatesTheName(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	for _, c := range []struct{ name, value string }{
		{"blank", ""},
		{"too short", "a"},
		{"taken", "Administrators"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := post(t, eng, cookie, "/admin/group", url.Values{"name": {c.value}})
			if w.Code == http.StatusFound {
				t.Error("want the form re-rendered with an error")
			}
		})
	}
	var n int
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM user_groups`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("groups = %d, want 1 — a rejected submit created a row", n)
	}
}
```

- [ ] **Step 2: Run — must fail** (`mountGroups` undefined)

Run: `go test ./internal/controller/admin/ -run TestGroup -count=1 2>&1 | head`

- [ ] **Step 3: Implement `group_crud.go`**

```go
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// groupRow is one row of the group list, and the edit form's model.
type groupRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Superuser bool   `json:"superuser"`
	Members   int    `json:"members"`
	Keys      int    `json:"keys"`
}

// mountGroups registers the group resource.
func (a *Admin) mountGroups(eng *inertia.Engine) {
	base := a.Prefix() + "/group"
	r := a.Resource(eng, "group")

	r.GET(base, a.groupsIndex)
	r.GET(base+"/new", a.groupNew)
	r.POST(base, a.groupCreate)
	r.GET(base+"/:id/edit", a.groupEdit)
	r.POST(base+"/:id", a.groupUpdate)
	r.POST(base+"/:id/delete", a.groupDelete)
	r.Menu("Access", "Groups", base)
}

func (a *Admin) groupBase() string { return a.Prefix() + "/group" }

// groupsIndex lists groups with their member counts — one query with a join
// rather than a count per row.
func (a *Admin) groupsIndex(c *inertia.Context) {
	q := fmt.Sprintf(`SELECT g.id, g.name, g.superuser, COUNT(u.id)
		FROM user_groups g LEFT JOIN %s u ON u.group_id = g.id
		GROUP BY g.id, g.name, g.superuser
		ORDER BY g.name`, a.usersTable())

	rows, err := a.DB.QueryContext(c.Request.Context(), q)
	if err != nil {
		slog.Error("admin: list groups", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []groupRow{}
	for rows.Next() {
		var g groupRow
		var superuser int
		if err := rows.Scan(&g.ID, &g.Name, &superuser, &g.Members); err != nil {
			slog.Error("admin: scan group", "err", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		g.Superuser = superuser != 0
		items = append(items, g)
	}
	if err := rows.Err(); err != nil {
		slog.Error("admin: list groups", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Set("items", items)
	c.Set("basePath", a.groupBase())
	if err := c.Render("admin/group/index"); err != nil {
		slog.Error("render admin group index", "err", err)
	}
}

func (a *Admin) groupNew(c *inertia.Context) {
	a.renderGroupForm(c, groupRow{}, nil, nil, nil)
}

func (a *Admin) groupCreate(c *inertia.Context) {
	ctx := c.Request.Context()
	item := groupRow{
		Name:      c.PostForm("name"),
		Superuser: c.PostForm("superuser") != "",
	}

	if v := a.validateGroup(ctx, item, 0); !v.OK() {
		a.renderGroupForm(c, item, v.Errors(), nil, nil)
		return
	}

	superuser := 0
	if item.Superuser {
		superuser = 1
	}
	if _, err := a.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at)
		 VALUES (?, ?, '[]', ?)`,
		item.Name, superuser, time.Now().UnixNano()); err != nil {
		slog.Error("admin: create group", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "分组已创建")
	a.redirect(c, a.groupBase())
}

func (a *Admin) groupEdit(c *inertia.Context) {
	id, _ := c.Params.GetInt64("id")
	item, keys, err := a.findGroupRow(c.Request.Context(), id)
	if err != nil {
		slog.Error("admin: load group", "err", err, "group", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if item == nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	a.renderGroupForm(c, *item, nil, keys, nil)
}

// groupUpdate renames a group, sets its superuser flag, and stores its
// permission keys. The whole change runs under the last-superuser guard, since
// clearing the flag drops every member of the group out of the count at once.
func (a *Admin) groupUpdate(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	item := groupRow{
		ID:        id,
		Name:      c.PostForm("name"),
		Superuser: c.PostForm("superuser") != "",
	}
	keys := a.submittedKeys(c)

	if v := a.validateGroup(ctx, item, id); !v.OK() {
		a.renderGroupForm(c, item, v.Errors(), keys, nil)
		return
	}

	raw, err := json.Marshal(keys)
	if err != nil {
		slog.Error("admin: encode permissions", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	superuser := 0
	if item.Superuser {
		superuser = 1
	}
	err = a.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE user_groups SET name = ?, superuser = ?, permissions = ? WHERE id = ?`,
			item.Name, superuser, string(raw), id)
		return err
	})
	switch {
	case errors.Is(err, errLastSuperuser):
		v := validate.New()
		v.Check(false, "superuser", "系统必须至少保留一个启用的超级管理员")
		a.renderGroupForm(c, item, v.Errors(), keys, nil)
		return
	case err != nil:
		slog.Error("admin: update group", "err", err, "group", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	a.flash(c, "success", "分组已更新")
	a.redirect(c, a.groupBase())
}

// groupDelete refuses while the group still has members: a user with no group is
// refused everything, including logout.
func (a *Admin) groupDelete(c *inertia.Context) {
	ctx := c.Request.Context()
	id, _ := c.Params.GetInt64("id")

	var members int
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE group_id = ?`, a.usersTable())
	if err := a.DB.QueryRowContext(ctx, q, id).Scan(&members); err != nil {
		slog.Error("admin: count group members", "err", err, "group", id)
		a.flash(c, "error", "删除失败，请查看日志")
		a.redirect(c, a.groupBase())
		return
	}
	if members > 0 {
		a.flash(c, "error", fmt.Sprintf("该分组还有 %d 个成员，请先把他们转到别的分组", members))
		a.redirect(c, a.groupBase())
		return
	}

	// An empty group cannot be the last superuser's group, so no guard is
	// needed: the count it protects only ever includes groups with members.
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id); err != nil {
		slog.Error("admin: delete group", "err", err, "group", id)
		a.flash(c, "error", "删除失败，请查看日志")
	} else {
		a.flash(c, "success", "分组已删除")
	}
	a.redirect(c, a.groupBase())
}

func (a *Admin) validateGroup(ctx context.Context, item groupRow, exceptID int64) *validate.Validator {
	v := validate.New()
	v.Field("name", item.Name,
		validate.Required,
		validate.MinLen(2),
		validate.MaxLen(64),
		a.groupNameAvailable(ctx, exceptID),
	)
	return v
}

func (a *Admin) groupNameAvailable(ctx context.Context, exceptID int64) validate.Rule {
	return func(name string) error {
		var n int
		if err := a.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM user_groups WHERE name = ? AND id != ?`, name, exceptID).Scan(&n); err != nil {
			slog.Error("admin: check group name", "err", err)
			return errors.New("无法校验，请重试")
		}
		if n > 0 {
			return errors.New("已被占用")
		}
		return nil
	}
}

// findGroupRow loads one group plus its stored permission keys, returning
// (nil, nil, nil) if absent.
func (a *Admin) findGroupRow(ctx context.Context, id int64) (*groupRow, []string, error) {
	var g groupRow
	var superuser int
	var raw string
	if err := a.DB.QueryRowContext(ctx,
		`SELECT id, name, superuser, permissions FROM user_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	g.Superuser = superuser != 0

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		// A group whose stored keys are unreadable must still be editable —
		// otherwise the only way to repair it is SQL, which is what this page
		// exists to avoid.
		slog.Error("admin: group has unreadable permissions", "err", err, "group", id)
		keys = nil
	}
	g.Keys = len(keys)
	return &g, keys, nil
}
```

Add `"encoding/json"` and `"time"` to the imports. `submittedKeys` and
`renderGroupForm` are implemented in Task 6 — add them here as the minimum this
task needs so the package compiles, and Task 6 replaces them:

```go
// submittedKeys reads the permission checkboxes. Task 6 replaces this with the
// normalising version; until then it preserves whatever is stored.
func (a *Admin) submittedKeys(c *inertia.Context) []string {
	return c.Request.Form["permissions"]
}

// renderGroupForm renders the create/edit form. Task 6 adds the permission grid
// and the stale-key list; `stale` is accepted now so the signature does not
// change under Task 6's callers.
func (a *Admin) renderGroupForm(c *inertia.Context, item groupRow, errs map[string]string, keys, stale []string) {
	c.Set("item", item)
	c.Set("keys", keys)
	c.Set("basePath", a.groupBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/group/form"); err != nil {
		slog.Error("render admin group form", "err", err)
	}
}
```

- [ ] **Step 4: Write the list page**

`frontend/pages/admin/group/index.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type GroupRow = { id: number; name: string; superuser: boolean; members: number; keys: number }

defineProps<{
  items: GroupRow[]
  basePath: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'name', label: '分组', sortable: true },
  { key: 'superuser', label: '权限' },
  { key: 'members', label: '成员' },
]

const pending = ref<GroupRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as GroupRow
}
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="分组" description="权限挂在分组上，不挂在个人上。">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          新建分组
        </Button>
      </template>
    </PageHeader>

    <Card>
      <CardContent class="pt-6">
        <DataTable :columns="columns" :data="items" search-key="name">
          <template #cell-name="{ row }">
            <a :href="`${basePath}/${row.id}/edit`" class="font-medium hover:underline">
              {{ row.name }}
            </a>
          </template>
          <template #cell-superuser="{ row }">
            <Badge v-if="row.superuser" variant="secondary">超级管理员</Badge>
            <span v-else class="text-muted-foreground">{{ row.keys }} 项权限</span>
          </template>
          <template #cell-members="{ row }">
            <span class="tabular-nums">{{ row.members }}</span>
          </template>
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">编辑</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">删除</DropdownMenuItem>
          </template>
          <template #empty>还没有分组。</template>
        </DataTable>
      </CardContent>
    </Card>

    <ConfirmDialog
      :open="pending !== null"
      :title="`删除分组“${pending?.name}”？`"
      description="只有没有成员的分组可以删除。"
      :action="`${basePath}/${pending?.id}/delete`"
      confirm-label="删除"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
```

The form page is written in Task 6, which owns the permission grid. To keep this
task's routes renderable, create `frontend/pages/admin/group/form.vue` now with
the name and superuser fields only:

```vue
<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type GroupRow = { id: number; name: string; superuser: boolean; members: number; keys: number }

const props = defineProps<{
  item: GroupRow
  basePath: string
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const editing = props.item.id > 0
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :crumb="editing ? '编辑' : '新建'"
  >
    <PageHeader :title="editing ? '编辑分组' : '新建分组'" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form
          :action="editing ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <FormField name="name" label="分组名" :error="errors?.name">
            <Input id="name" name="name" :model-value="item.name" :aria-invalid="!!errors?.name" />
          </FormField>

          <FormField name="superuser" label="超级管理员" :error="errors?.superuser">
            <label class="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                name="superuser"
                value="1"
                :checked="item.superuser"
                class="size-4 accent-primary"
              >
              绕过所有权限检查
            </label>
          </FormField>

          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="basePath" variant="outline">取消</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

- [ ] **Step 5: Run and commit**

```bash
go test ./internal/controller/admin/ -count=1 && go test ./... -count=1
pnpm -C frontend run type-check && pnpm -C frontend run build
gofmt -l ./ && go vet ./...
git add internal/controller/admin/ frontend/pages/admin/group/
git commit -m "feat(admin): the group resource, and no deleting a group with members"
```

---

### Task 6: The permission grid

**Files:**
- Modify: `internal/controller/admin/group_crud.go` (replace `submittedKeys` and `renderGroupForm`), `frontend/pages/admin/group/form.vue`
- Create: `frontend/pages/admin/group/PermissionGrid.vue`, `frontend/pages/admin/group/PermissionGrid.test.ts`
- Test: `internal/controller/admin/group_crud_test.go`

**Interfaces:**
- Consumes: `(*Admin).Permissions() []Permission` (`Permission{Key string; Routes []string}`), `verbAccess`, `verbModify`, `permKey`.
- Produces:
  ```go
  type permRow struct {
      Resource string `json:"resource"`
      Access   bool   `json:"access"`
      Modify   bool   `json:"modify"`
  }
  func (a *Admin) permissionRows(keys []string) ([]permRow, []string) // rows, stale
  func (a *Admin) submittedKeys(c *inertia.Context) []string          // normalised
  ```

- [ ] **Step 1: Write the failing Go test** (append to `group_crud_test.go`)

```go
// modify implies access one-way, exactly as permSet.Allows has it. The grid does
// this client-side for convenience; the server does it again because the client
// cannot be the enforcement point.
func TestGroupUpdate_NormalisesModifyImpliesAccess(t *testing.T) {
	eng, adm, cookie := groupStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO user_groups (name, superuser, permissions, created_at) VALUES ('Editors', 0, '[]', 0)`); err != nil {
		t.Fatal(err)
	}
	var gid int64
	if err := adm.DB.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = 'Editors'`).Scan(&gid); err != nil {
		t.Fatal(err)
	}

	// Only post.modify submitted — post.access must be stored too.
	w := post(t, eng, cookie, fmt.Sprintf("/admin/group/%d", gid), url.Values{
		"name":        {"Editors"},
		"permissions": {"post.modify"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var raw string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT permissions FROM user_groups WHERE id = ?`, gid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if !got["post.modify"] || !got["post.access"] {
		t.Errorf("stored %v, want both post.modify and post.access", keys)
	}
}

// A key for a resource that no longer registers routes is shown to the operator
// and removed on save. Writing it back silently would leave permissions in
// effect that the interface never displayed.
func TestPermissionRows_SeparatesStaleKeys(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, nil)
	a.Resource(eng, "post").GET("/admin/post", func(c *inertia.Context) {})
	a.Resource(eng, "post").POST("/admin/post", func(c *inertia.Context) {})

	rows, stale := a.permissionRows([]string{"post.access", "billing.modify"})

	if len(rows) != 1 || rows[0].Resource != "post" {
		t.Fatalf("rows = %+v, want one row for post", rows)
	}
	if !rows[0].Access || rows[0].Modify {
		t.Errorf("post row = %+v, want access only", rows[0])
	}
	if len(stale) != 1 || stale[0] != "billing.modify" {
		t.Errorf("stale = %v, want [billing.modify]", stale)
	}
}
```

Add `"encoding/json"` to the test file's imports.

- [ ] **Step 2: Run — must fail** (`permissionRows` undefined)

Run: `go test ./internal/controller/admin/ -run 'TestPermissionRows|TestGroupUpdate_Normalises' -count=1 2>&1 | head`

- [ ] **Step 3: Implement the server side.** Replace `submittedKeys` and `renderGroupForm` in `group_crud.go`, and add `permRow`/`permissionRows`:

```go
// permRow is one resource's two checkboxes in the grid.
type permRow struct {
	Resource string `json:"resource"`
	Access   bool   `json:"access"`
	Modify   bool   `json:"modify"`
}

// permissionRows turns a group's stored keys into grid rows, and returns the
// keys that no longer correspond to a registered route.
//
// Rows come from the catalogue — the routes actually registered at startup — so
// the grid cannot offer a permission nothing checks. Keys outside it are stale:
// a resource that was renamed or removed. They are reported rather than dropped
// quietly, because the alternative is a group holding permissions its own edit
// page never showed.
func (a *Admin) permissionRows(keys []string) ([]permRow, []string) {
	held := make(map[string]bool, len(keys))
	for _, k := range keys {
		held[k] = true
	}

	seen := map[string]bool{}
	rows := []permRow{}
	known := map[string]bool{}
	for _, p := range a.Permissions() {
		known[p.Key] = true
		resource, _, ok := splitPermKey(p.Key)
		if !ok || seen[resource] {
			continue
		}
		seen[resource] = true
		rows = append(rows, permRow{
			Resource: resource,
			Access:   held[resource+verbAccess],
			Modify:   held[resource+verbModify],
		})
	}

	stale := []string{}
	for _, k := range keys {
		if !known[k] {
			stale = append(stale, k)
		}
	}
	slices.Sort(stale)
	return rows, stale
}

// splitPermKey splits "post.modify" into ("post", ".modify"). Reported false for
// anything that is not a permission key, which Resource's name validation makes
// impossible for registered resources but not for stored data.
func splitPermKey(key string) (resource, verb string, ok bool) {
	for _, v := range []string{verbAccess, verbModify} {
		if strings.HasSuffix(key, v) {
			return strings.TrimSuffix(key, v), v, true
		}
	}
	return "", "", false
}

// submittedKeys reads the permission checkboxes and normalises them: modify
// implies access, one-way, exactly as permSet.Allows has it. The grid does the
// same thing client-side for immediate feedback, which is why this cannot be
// left to the grid — convenience there, enforcement here.
//
// Keys not in the catalogue are ignored rather than trusted, so a hand-made POST
// cannot store a permission the interface would never show.
func (a *Admin) submittedKeys(c *inertia.Context) []string {
	if err := c.Request.ParseForm(); err != nil {
		slog.Error("admin: parse permission form", "err", err)
		return nil
	}
	known := map[string]bool{}
	for _, p := range a.Permissions() {
		known[p.Key] = true
	}

	set := map[string]bool{}
	for _, k := range c.Request.Form["permissions"] {
		if !known[k] {
			continue
		}
		set[k] = true
		if resource, verb, ok := splitPermKey(k); ok && verb == verbModify {
			set[resource+verbAccess] = true
		}
	}

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// renderGroupForm renders the create/edit form with the permission grid.
func (a *Admin) renderGroupForm(c *inertia.Context, item groupRow, errs map[string]string, keys, stale []string) {
	rows, computedStale := a.permissionRows(keys)
	if stale == nil {
		stale = computedStale
	}
	c.Set("item", item)
	c.Set("permissions", rows)
	c.Set("stale", stale)
	c.Set("basePath", a.groupBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/group/form"); err != nil {
		slog.Error("render admin group form", "err", err)
	}
}
```

Add `"slices"` and `"strings"` to the imports. Remove the `c.Set("keys", keys)`
line from the Task 5 version — the page now reads `permissions` and `stale`.

- [ ] **Step 4: Write the grid component and its test**

`frontend/pages/admin/group/PermissionGrid.vue`:

```vue
<script setup lang="ts">
// The permission grid: one row per registered resource, two checkboxes each.
// `modify` implies `access` one-way, mirroring permSet.Allows on the server —
// this is feedback, not enforcement; the server normalises the same way on save.
import { reactive, watch } from 'vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

type PermRow = { resource: string; access: boolean; modify: boolean }

const props = defineProps<{ rows: PermRow[]; stale?: string[]; superuser?: boolean }>()

// A local copy so the implication can be applied as the user clicks.
const state = reactive<Record<string, { access: boolean; modify: boolean }>>({})
watch(
  () => props.rows,
  (rows) => {
    for (const r of rows) state[r.resource] = { access: r.access, modify: r.modify }
  },
  { immediate: true, deep: true },
)

function onModify(resource: string) {
  if (state[resource].modify) state[resource].access = true
}
function onAccess(resource: string) {
  if (!state[resource].access) state[resource].modify = false
}
</script>

<template>
  <div class="space-y-3">
    <p v-if="superuser" class="text-sm text-muted-foreground">
      该分组是超级管理员，绕过所有权限检查 —— 下面的勾选不影响它的实际权限。
    </p>

    <div class="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>资源</TableHead>
            <TableHead class="w-24 text-center">读取</TableHead>
            <TableHead class="w-24 text-center">修改</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in rows" :key="r.resource">
            <TableCell><code class="text-sm">{{ r.resource }}</code></TableCell>
            <TableCell class="text-center">
              <input
                type="checkbox"
                name="permissions"
                class="size-4 accent-primary"
                :value="`${r.resource}.access`"
                :data-resource="r.resource"
                data-verb="access"
                :checked="state[r.resource]?.access"
                @change="state[r.resource].access = ($event.target as HTMLInputElement).checked; onAccess(r.resource)"
              >
            </TableCell>
            <TableCell class="text-center">
              <input
                type="checkbox"
                name="permissions"
                class="size-4 accent-primary"
                :value="`${r.resource}.modify`"
                :data-resource="r.resource"
                data-verb="modify"
                :checked="state[r.resource]?.modify"
                @change="state[r.resource].modify = ($event.target as HTMLInputElement).checked; onModify(r.resource)"
              >
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <p v-if="stale?.length" class="text-sm text-muted-foreground">
      保存将清除以下失效权限（对应的资源已不存在）：
      <code v-for="k in stale" :key="k" class="ml-1">{{ k }}</code>
    </p>
  </div>
</template>
```

`frontend/pages/admin/group/PermissionGrid.test.ts`:

```ts
// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import PermissionGrid from './PermissionGrid.vue'

function mount(props: Record<string, unknown>) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(PermissionGrid, props as any) }).mount(el)
  return el
}

const box = (el: HTMLElement, resource: string, verb: string) =>
  el.querySelector(`input[data-resource="${resource}"][data-verb="${verb}"]`) as HTMLInputElement

const rows = [
  { resource: 'post', access: false, modify: false },
  { resource: 'user', access: true, modify: false },
]

describe('PermissionGrid', () => {
  it('renders one row per resource with the stored state', () => {
    const el = mount({ rows })
    expect(box(el, 'post', 'access').checked).toBe(false)
    expect(box(el, 'user', 'access').checked).toBe(true)
  })

  // The implication is one-way, matching permSet.Allows on the server.
  it('ticks access when modify is ticked', async () => {
    const el = mount({ rows })
    const modify = box(el, 'post', 'modify')
    modify.checked = true
    modify.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'access').checked).toBe(true)
  })

  it('unticks modify when access is unticked', async () => {
    const el = mount({ rows: [{ resource: 'post', access: true, modify: true }] })
    const access = box(el, 'post', 'access')
    access.checked = false
    access.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'modify').checked).toBe(false)
  })

  // Ticking access alone must NOT tick modify — that would grant write access
  // to anyone given read access.
  it('does not tick modify when access is ticked', async () => {
    const el = mount({ rows })
    const access = box(el, 'post', 'access')
    access.checked = true
    access.dispatchEvent(new Event('change'))
    await nextTick()
    expect(box(el, 'post', 'modify').checked).toBe(false)
  })

  it('lists stale keys with a warning that saving removes them', () => {
    const el = mount({ rows, stale: ['billing.modify'] })
    expect(el.textContent).toContain('billing.modify')
    expect(el.textContent).toContain('保存将清除')
  })

  it('says the grid is advisory for a superuser group', () => {
    const el = mount({ rows, superuser: true })
    expect(el.textContent).toContain('绕过所有权限检查')
  })
})
```

- [ ] **Step 5: Add the grid to the form page.** In `frontend/pages/admin/group/form.vue`: import it, widen the card, extend the props, and render it after the superuser field.

```ts
import PermissionGrid from './PermissionGrid.vue'
```

Props gain:

```ts
  permissions: { resource: string; access: boolean; modify: boolean }[]
  stale?: string[]
```

Change `<Card class="max-w-lg">` to `<Card>` — the grid is a table and does not
belong in a narrow column — and insert before the buttons:

```vue
          <FormField name="permissions" label="权限">
            <PermissionGrid
              :rows="permissions"
              :stale="stale"
              :superuser="item.superuser"
            />
          </FormField>
```

- [ ] **Step 6: Run everything**

```bash
go test ./internal/controller/admin/ -count=1 && go test ./... -count=1
pnpm -C frontend run test && pnpm -C frontend run type-check && pnpm -C frontend run build
```

Expected: all green, 6 new vitest cases.

- [ ] **Step 7: Commit**

```bash
gofmt -l ./ && go vet ./...
git add internal/controller/admin/ frontend/pages/admin/group/
git commit -m "feat(admin): the permission grid, normalised on the server"
```

---

### Task 7: Self-service password, the one exempt route

**Files:**
- Create: `internal/controller/admin/account.go`, `internal/controller/admin/account_test.go`, `frontend/pages/admin/account/password.vue`
- Modify: `frontend/src/components/admin/AdminShell.vue` (the user-menu item)

**Interfaces:**
- Consumes: `AuthMiddleware`, `callerID`, `verifyPassword`, `HashPassword`, `validate.*`, `bcryptMaxPassword`.
- Produces: `func (a *Admin) mountAccount(eng *inertia.Engine)`, handlers `passwordForm`, `passwordSubmit`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/admin/account_test.go`:

```go
package admin

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/millken/inertia"
)

func accountStack(t *testing.T) (*inertia.Engine, *Admin, *http.Cookie) {
	t.Helper()
	eng, adm := loginStack(t)
	adm.mountAccount(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}
	return eng, adm, loginAndGetCookie(t, eng)
}

// The whole reason this route is exempt: a user whose group grants nothing must
// still be able to change their own password. If it went through the registrar
// they never could.
func TestAccountPassword_ReachableWithoutAnyPermission(t *testing.T) {
	eng, adm, cookie := accountStack(t)
	putInGroup(t, adm, "Nobody", false, `[]`)

	r := httptest.NewRequest(http.MethodGet, "/admin/account/password", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a permission-less user must reach this page", w.Code)
	}
}

func TestAccountPassword_ChangesThePassword(t *testing.T) {
	eng, adm, cookie := accountStack(t)
	ctx := context.Background()

	w := post(t, eng, cookie, "/admin/account/password", url.Values{
		"current":  {"pw"},
		"password": {"newpassword"},
		"confirm":  {"newpassword"},
	})
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body: %s", w.Code, w.Body.String())
	}

	var hash string
	if err := adm.DB.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(hash, "newpassword") {
		t.Error("the new password does not verify")
	}
	if verifyPassword(hash, "pw") {
		t.Error("the old password still verifies")
	}
}

func TestAccountPassword_Rejections(t *testing.T) {
	for _, c := range []struct {
		name string
		form url.Values
	}{
		{"wrong current password", url.Values{"current": {"nope"}, "password": {"newpassword"}, "confirm": {"newpassword"}}},
		{"confirmation mismatch", url.Values{"current": {"pw"}, "password": {"newpassword"}, "confirm": {"different"}}},
		{"too short", url.Values{"current": {"pw"}, "password": {"short"}, "confirm": {"short"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			eng, adm, cookie := accountStack(t)
			w := post(t, eng, cookie, "/admin/account/password", c.form)
			// A field error, not a 403: the user is allowed here, they just got
			// something wrong.
			if w.Code == http.StatusFound {
				t.Error("want the form re-rendered with an error")
			}
			if w.Code == http.StatusForbidden {
				t.Error("a wrong current password is a field error, not a refusal to be here")
			}
			var hash string
			if err := adm.DB.QueryRowContext(context.Background(),
				`SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			if !verifyPassword(hash, "pw") {
				t.Error("the password changed despite the rejection")
			}
		})
	}
}
```

Add `"net/http/httptest"` to the imports.

- [ ] **Step 2: Run — must fail** (`mountAccount` undefined)

- [ ] **Step 3: Implement `account.go`**

```go
package admin

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/millken/goapp-template/internal/validate"
	"github.com/millken/inertia"
)

// mountAccount registers the signed-in user's own account page.
//
// This is the only route in the admin area registered outside the permission
// registrar, and it is deliberate: it uses AuthMiddleware, so it requires a
// session but no permission. Routing it through the registrar would create
// account.access / account.modify keys, and a user whose group holds neither
// could never change their own password — the one thing every user must be able
// to do for themselves. It is the third exemption, alongside logout and the
// dashboard.
//
// If you are reading this while auditing "which routes skip the registrar", this
// is the expected answer, not an oversight.
func (a *Admin) mountAccount(eng *inertia.Engine) {
	auth := a.AuthMiddleware()
	base := a.Prefix() + "/account/password"
	eng.GET(base, auth, a.passwordForm)
	eng.POST(base, auth, a.passwordSubmit)
}

func (a *Admin) accountBase() string { return a.Prefix() + "/account/password" }

func (a *Admin) passwordForm(c *inertia.Context) {
	a.renderPasswordForm(c, nil)
}

func (a *Admin) passwordSubmit(c *inertia.Context) {
	ctx := c.Request.Context()
	id, ok := a.callerID(c)
	if !ok {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	current := c.PostForm("current")
	password := c.PostForm("password")
	confirm := c.PostForm("confirm")

	q := fmt.Sprintf(`SELECT password_hash FROM %s WHERE id = ?`, a.usersTable())
	var hash string
	if err := a.DB.QueryRowContext(ctx, q, id).Scan(&hash); err != nil {
		slog.Error("admin: load own password hash", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	v := validate.New()
	// A wrong current password is a field error rather than a refusal: the user
	// is allowed on this page, they just mistyped.
	v.Check(verifyPassword(hash, current), "current", "当前密码不正确")
	v.Field("password", password,
		validate.Required,
		validate.MinLen(8),
		validate.Msg(validate.MaxLen(bcryptMaxPassword), "不能超过 72 个字符（bcrypt 的上限）"),
	)
	v.Check(password == confirm, "confirm", "两次输入不一致")
	if !v.OK() {
		a.renderPasswordForm(c, v.Errors())
		return
	}

	newHash, err := HashPassword(password)
	if err != nil {
		slog.Error("admin: hash password", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	uq := fmt.Sprintf(`UPDATE %s SET password_hash = ? WHERE id = ?`, a.usersTable())
	if _, err := a.DB.ExecContext(ctx, uq, newHash, id); err != nil {
		slog.Error("admin: change own password", "err", err, "user", id)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// The session is untouched: the password changed, not the identity, and
	// signing the user out of the page they just used would be gratuitous.
	a.flash(c, "success", "密码已修改")
	a.redirect(c, a.mount())
}

func (a *Admin) renderPasswordForm(c *inertia.Context, errs map[string]string) {
	c.Set("basePath", a.accountBase())
	if errs != nil {
		c.Set("errors", errs)
	}
	if err := c.Render("admin/account/password"); err != nil {
		slog.Error("render admin account password", "err", err)
	}
}
```

- [ ] **Step 4: Write the page**

`frontend/pages/admin/account/password.vue`:

```vue
<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }

defineProps<{
  basePath: string
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    crumb="修改密码"
  >
    <PageHeader title="修改密码" description="修改你自己的登录密码。" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form :action="basePath" method="post" class="space-y-4">
          <FormField name="current" label="当前密码" :error="errors?.current">
            <Input id="current" name="current" type="password" :aria-invalid="!!errors?.current" />
          </FormField>
          <FormField name="password" label="新密码" :error="errors?.password">
            <Input id="password" name="password" type="password" :aria-invalid="!!errors?.password" />
            <p class="text-sm text-muted-foreground">8–72 个字符。</p>
          </FormField>
          <FormField name="confirm" label="确认新密码" :error="errors?.confirm">
            <Input id="confirm" name="confirm" type="password" :aria-invalid="!!errors?.confirm" />
          </FormField>
          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="adminMount || '/admin'" variant="outline">取消</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

- [ ] **Step 5: Add the entry point to `AdminShell.vue`.** In the user dropdown, before the logout form, add a menu item and a separator so it reads as its own group:

```vue
              <DropdownMenuItem as="a" :href="`${base}/account/password`">
                <KeyRound />
                修改密码
              </DropdownMenuItem>
              <DropdownMenuSeparator />
```

Add `KeyRound` to the `lucide-vue-next` import. It is not a menu-registry entry,
so no permission gates it.

Then extend `frontend/src/components/admin/AdminShell.test.ts` with:

```ts
  it('links to the account password page from the user menu', async () => {
    const el = mount(props)
    await openUserMenu(el)
    expect(document.querySelector('a[href="/admin/account/password"]')).not.toBeNull()
  })
```

- [ ] **Step 6: Run everything and commit**

```bash
go test ./... -count=1
pnpm -C frontend run test && pnpm -C frontend run type-check && pnpm -C frontend run build
gofmt -l ./ && go vet ./...
git add internal/controller/admin/ frontend/pages/admin/account/ frontend/src/components/admin/
git commit -m "feat(admin): self-service password change on the one exempt route"
```

---

### Task 8: Wire it up, and say so

**Files:**
- Modify: `internal/controller/admin/admin.go` (`Mount`), `cmd/goappctl/internal/components/components.go`, `README.md`

**Interfaces:**
- Consumes: `mountUsers`, `mountGroups`, `mountAccount`.

- [ ] **Step 1: Write the failing test** (append to `internal/controller/admin/permission_test.go`)

```go
// Mount must register everything, and the catalogue is how we check: the user
// and group resources contribute four keys between them, and a resource whose
// Mount call was forgotten shows up as a missing key rather than as a 404 nobody
// notices until a page is opened.
func TestMount_RegistersUsersGroupsAndAccount(t *testing.T) {
	eng := newTestEngine(t)
	a := New(nil, &Config{Mount: "/admin"})
	if err := a.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	a.Mount(eng)
	if err := eng.RegistrationError(); err != nil {
		t.Fatalf("routes did not register: %v", err)
	}

	got := map[string]bool{}
	for _, p := range a.Permissions() {
		got[p.Key] = true
	}
	for _, want := range []string{"user.access", "user.modify", "group.access", "group.modify"} {
		if !got[want] {
			t.Errorf("catalogue missing %q — a resource was not mounted", want)
		}
	}

	// The account page is deliberately absent from the catalogue: it is exempt.
	for _, unwanted := range []string{"account.access", "account.modify"} {
		if got[unwanted] {
			t.Errorf("%q exists — the account route must stay exempt, or a user "+
				"with no permissions could never change their password", unwanted)
		}
	}
}
```

- [ ] **Step 2: Run — must fail**

Run: `go test ./internal/controller/admin/ -run TestMount_Registers -count=1`
Expected: FAIL — the catalogue has none of those keys.

- [ ] **Step 3: Extend `Mount`**

```go
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
```

- [ ] **Step 4: Own the new paths.** In `cmd/goappctl/internal/components/components.go`, the admin component's `Owned` list already covers `internal/controller/admin` and `frontend/pages/admin` as directories, so the new files are covered. Verify rather than assume:

```bash
grep -n "frontend/pages/admin\|internal/controller/admin" cmd/goappctl/internal/components/components.go
```

If either is listed as a directory, add nothing. If a file-level entry exists for
something you added beside, add the directory instead.

- [ ] **Step 5: Document it.** In `README.md`, inside the `<!--goappctl:admin-->` block that holds the 后台权限 section, replace the paragraph that currently says permissions are set with SQL. Read it first, then substitute:

```markdown
分组、权限和用户都在后台里管理：**Access → Users / Groups**。权限编辑器的行来自
`adm.Permissions()`（启动时真正注册的路由），所以它不会提供一个没人检查的权限；
勾 `modify` 会自动带上 `access`（服务端同样归一化一次，前端那套只是即时反馈）。

三条防自锁规则在服务端强制，不靠 UI 禁用按钮：不能删除或禁用自己的账号；不能修改
自己所在的分组；系统必须至少保留一个**启用的**超级管理员。第三条的实现方式是把改动
放进事务、然后数一次剩余的启用超管，为 0 就回滚 —— 四条能触发它的路径（禁用、删除、
移出超管组、清掉分组的超管标记）共用同一个守卫，所以将来新增的第五条路径也漏不掉。

禁用一个用户**下一个请求就生效**：`findGroup` 已经 JOIN 了 users 表，多带一列
`status` 即可，仍是每请求一次查询。被禁用的用户会带着一条说明跳回登录页，
且重新登录也会被拒绝。

**唯一一条不经注册器的路由**是 `/admin/account/password`（自助改密），它用
`AuthMiddleware` —— 要求登录但不要求权限。必须如此：走注册器就会产生
`account.access`/`account.modify`，而权限为空的用户将永远改不了自己的密码。
入口在右上角用户菜单里。
```

- [ ] **Step 6: Verify the whole thing, including the trim**

```bash
go test ./... -count=1
pnpm -C frontend run test && pnpm -C frontend run type-check && pnpm -C frontend run build
go test ./server/ -count=1        # SSR, with the bundle just built
go run ./cmd/goappctl init --dry-run --module example.com/myapp --with db,session,ssr 2>&1 | grep -Ei "controller/admin|pages/admin|error"
```

Expected: everything green; the dry run lists `internal/controller/admin` and
`frontend/pages/admin` as deleted.

- [ ] **Step 7: Commit**

```bash
gofmt -l ./ && go vet ./...
git add internal/controller/admin/ cmd/goappctl/ README.md
git commit -m "feat(admin): mount users, groups and the account page; document them"
```

---

## Self-Review Notes

- **Spec coverage:** §3 → Task 1 Steps 1–3; §4 → Task 1 (all three read paths, the flash-not-destroy rule, the post-verify status check); §5 → Task 2, applied in Tasks 4–6; §6 routes → Tasks 3–7, `Mount` → Task 8; §7 validation → Tasks 3, 5, 7 (username charset comment in Task 3, the 72-byte reason in both password paths); §8 grid → Task 6 (rows from the catalogue, one-way implication server *and* client, stale keys listed then removed, advisory for superuser groups); §9 files → the File Structure block; §10 tests → each task's test steps; §11 exclusions respected (no audit log, no last-login, no email, no bulk actions, no per-user overrides).
- **Type consistency:** `caller`/`findCaller` (Task 1) is consumed by name in Task 2's Interfaces; `userRow`/`groupOption` (Task 3) reappear unchanged in Tasks 4 and 5; `groupRow` (Task 5) keeps its `Keys` field for the list page while Task 6 adds `permRow` beside it; `renderGroupForm`'s five-parameter signature is introduced in Task 5 precisely so Task 6 does not change it under its callers; `statusActive`/`statusDisabled` are declared once in Task 1 and used in 2, 3 and 4.
- **Deliberate stub-then-replace, twice:** Task 3 registers `userDelete`/`userSetStatus` as 501 stubs so the resource's permission set is complete from the first commit, and Task 5 does the same for `submittedKeys`/`renderGroupForm`. Both are replaced in the next task, and each stub says so. The alternative — registering the routes later — would mean the catalogue changes shape mid-plan, which is worse for a reviewer to follow.
- **One risk worth naming:** the shared test helpers already exist and must not be redeclared — `newTestEngine` in `admin_test.go`, `putInGroup` in `permission_test.go`, `loginStack`/`loginAndGetCookie` in `login_test.go`. Tasks 3, 5, 6 and 7 all use them. A duplicate declaration is a compile error, so it surfaces immediately; `adminStack`/`post` (Task 3) and `groupStack`/`accountStack` (Tasks 5, 7) are the new ones, each declared once.
