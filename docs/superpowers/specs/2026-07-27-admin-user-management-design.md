# Admin User & Permission Management — Design Spec

Date: 2026-07-27 · Status: Validated — approved for planning · Depends on: the
`admin` component (permissions, the menu registry, the UI composites)

This is stage 2 of the admin permission work. Stage 1 (`2026-07-26-admin-permissions-design.md`)
made permissions enforceable and said outright that there is no interface for
editing them; the UI conventions spec (`2026-07-26-admin-ui-conventions-design.md`)
built the composites these pages are assembled from. This spec is the interface.

## 1. Goal

Give the admin area the screens that make its own permission model usable:
create and edit admin users, assign them to groups, disable and re-enable them,
reset their passwords, and edit what a group may do.

What exists today: a `users` table with a `group_id`, a `user_groups` table
holding a JSON array of permission keys, and exactly one way to put a person in
the system — `myapp admin create-user --group`. Changing anyone's permissions
means writing SQL. The README says so in as many words.

**Non-goal, stated up front:** this remains an *admin* user system. There is no
public registration, no email, no profile. The `users` table backs the admin
area's login and nothing else.

## 2. Decisions

| Question | Decision |
|---|---|
| Where the code lives | **In `internal/controller/admin/`**, part of the admin component — the template ships a working admin area rather than instructions for generating one |
| Self-lockout protection | **Three rules, enforced server-side**: no deleting or disabling yourself; no changing your own group; at least one enabled superuser user must remain |
| How rule 3 is enforced | **One shared guard**, not four bespoke checks: apply the change in a transaction, count enabled superusers, roll back if zero |
| Disabling a signed-in user | **Effective on their next request.** `findGroup`'s existing query gains a `status` column — still one lookup per request |
| Deleting a group with members | **Refused**, with the member count in the message. A member-less user is refused everything including logout, which stage 1 spent effort fixing |
| Passwords | **Admin reset + self-service change.** The self-service page is permission-exempt, or a user with no permissions could never change their own password |
| Stale permission keys | **Listed and removed.** The editor shows keys no longer in the catalogue under "saving will remove these"; after saving, the key set equals the checkbox state |
| Permission grid rows | From `adm.Permissions()` — the routes actually registered at startup |

## 3. Schema: migration `004_user_status`

```sql
ALTER TABLE users ADD COLUMN status INTEGER NOT NULL DEFAULT 1;
```

`1` = active, `0` = disabled. SQLite fills existing rows with a constant
`DEFAULT` on `ADD COLUMN`, so **no backfill statement is needed** — unlike
`003`, whose `group_id` was nullable and therefore left existing users
group-less until a backfill was added. The distinction is worth a comment in the
file so the next migration author does not copy the wrong precedent.

`004_user_status.down.sql` drops the column and states what that discards (every
disabled flag; users come back enabled).

The migration writes the literal `users` table. The `[admin] users_table`
setting redirects runtime lookups only — embedded SQL cannot read config — so
pointing it elsewhere makes that table's schema the operator's responsibility,
including `status`.

## 4. Disabling takes effect immediately

`findGroup` currently selects `u.username, g.superuser, g.permissions`. It gains
`u.status`, and returns it. Still one query per request — the constraint stage 1
established and stage 1's reviewer verified by instrumentation.

Three places read it, and all three have to change together — the struct is the
one an implementer forgets, because the other two stop compiling without it:

- `User` gains `Status int`.
- `findUser`'s `SELECT id, username, password_hash` gains `status`, so
  `authenticate` has something to read.
- `authenticate` refuses a disabled account **after `verifyPassword` succeeds**,
  never before. Checking earlier would make a disabled account distinguishable
  from a wrong password by timing — the same leak the existing
  `dummyPasswordHash` compare exists to prevent on the unknown-user path.

Because the refusal happens only once the correct password has been supplied,
the error may say plainly that the account is disabled rather than reusing
`errInvalidCredentials`: whoever sees it already proved they hold the
credential, so nothing leaks, and "invalid credentials" would send the real
owner hunting for a password problem that does not exist.

`resolve`, on `status = 0`, flashes `error` → `该账号已被禁用。` and redirects to
the login path. Not a bare 403: a disabled user would otherwise stare at an
empty page forever with nothing to act on. `authenticate` also refuses a
disabled user, so logging back in fails too.

**It does not destroy the session, and that is deliberate.** A flash *is* session
state (`session/flash.go` stores it under a `_flash:` key), so destroying the
session would discard the very message being staged — the two cannot both
happen. Nor is destroying needed: `resolve` refuses the session on every
subsequent request, so the credential is already inert. The visible consequence
is that re-enabling a user restores their existing session rather than forcing a
fresh login, which is the better behaviour anyway.

An implementer reaching for `sess.Destroy` here should stop: the ordering does
not work in either direction, since `Destroy` also clears the response cookie
the flash would have to travel in.

This is a third outcome alongside the existing two, and the distinction must
survive: `errNoGroup` → 403 (a permissions answer), any other error → 500 (an
infrastructure failure), `status = 0` → bounce to login (an account answer).

## 5. The three guardrails

In `internal/controller/admin/guardrails.go`, shared by every mutating handler.

**Rule 1 — no deleting or disabling yourself.** The acting user's id comes from
the session, which `resolve` already read; comparing it to the target id is free.

**Rule 2 — no changing your own group.** Editing your own user is allowed
(username, password); submitting a different `group_id` for yourself is refused.
Rejecting the field rather than hiding it in the UI, because the UI is not the
enforcement point.

**Rule 3 — at least one enabled superuser must remain.** Four different changes
can violate it: disabling a user, deleting a user, moving a user out of a
superuser group, and clearing a group's `superuser` flag. Rather than four
bespoke pre-checks — where the fifth path someone invents later is a silent hole
— the change runs inside a transaction and is then verified:

```sql
SELECT COUNT(*) FROM users u JOIN user_groups g ON g.id = u.group_id
 WHERE g.superuser = 1 AND u.status = 1
```

**The count runs inside the same transaction, after the mutation.** That is the
whole mechanism, not an implementation detail: the `UPDATE`/`DELETE` is staged,
the count then reads the state that change produced, and a zero rolls it back.
SQLite's default isolation makes a transaction see its own uncommitted writes,
so clearing a group's `superuser` flag correctly drops every member of that group
out of the count in one go.

Moving the count outside the transaction, or before the mutation, turns it back
into the pre-check this design rejects — a prediction of the resulting state
rather than a reading of it, and predictions are what miss the fifth path.

Zero means roll back and report. One guard covers every path, present and
future.

Rules 1 and 2 answer 403 with a flash naming the rule. Rule 3 answers by
re-rendering the form with a field-level error, since it is a consequence of the
submitted values rather than a forbidden action.

## 6. Routes

Both resources go through the registrar, so each route's permission exists by
construction:

```go
r := adm.Resource(eng, "user")
r.GET(base, ct.Index); r.GET(base+"/new", ct.New); r.POST(base, ct.Create)
r.GET(base+"/:id/edit", ct.Edit); r.POST(base+"/:id", ct.Update)
r.POST(base+"/:id/delete", ct.Delete); r.POST(base+"/:id/status", ct.SetStatus)
r.Menu("Access", "Users", base)
```

`POST /:id/status` carries a `status` form field of `0` or `1` rather than
toggling whatever it finds, so a double-submitted form cannot flip a user back
on. One handler, but **the two directions are not symmetric**, and the handler
branches on that:

| | Rule 1 (self) | Rule 3 (last superuser) |
|---|---|---|
| `status=0` (disable) | refused | checked |
| `status=1` (enable) | allowed — and a no-op, since a disabled user cannot reach this route to begin with | not applicable: enabling can only increase the count |

`group` is the same shape, minus `status`, plus the permission grid on its edit
page. Both sit in the `Access` menu section.

**The one exempt route.** `GET|POST /admin/account/password` uses
`AuthMiddleware`, not the registrar — the third exemption after logout and the
dashboard. It must not be permission-gated: a user whose group grants nothing
would otherwise be unable to change their own password. The reason belongs in a
comment beside the registration and in the README, because "a route registered
outside the registrar" is exactly the shape stage 1 exists to prevent, and a
future reader must be able to tell this apart from a mistake.

Its entry point is the topbar user menu, not the sidebar: `AdminShell` gains a
"修改密码" item beside "Log out". Nothing is added to the menu registry, so no
permission gates it.

## 7. Validation

All server-side, through `internal/validate`. No client-side validation, as
established.

| Field | Rules |
|---|---|
| Username | required, 3–64, `^[a-zA-Z0-9._-]+$`, unique |
| Password | 8–72 |
| Group name | required, 2–64, unique |
| Group id (on a user) | required, must exist |

The password ceiling is not arbitrary, and the unit is **bytes, not characters**:
`x/crypto/bcrypt` returns `ErrPasswordTooLong` past 72 bytes rather than
truncating. A character-based check therefore lets a non-ASCII password through
— 30 Chinese characters are 90 bytes — and it fails at hashing time as a 500 the
user cannot act on. The rule has to count bytes.

(Corrected after implementation: the first draft of this spec said bcrypt
truncates. It does not, in the version this project pins, and the difference is
the one that matters — truncation would be a silent weakening, an error is a
crash.)

The username charset is **ASCII-only on purpose**: admin accounts are created by
an operator, not chosen by a visitor, and they appear in log lines, CLI
arguments and `create-user` invocations where a Unicode identifier is a nuisance
rather than a feature. Recorded here so a later reader does not file it as a bug.

Uniqueness is a handler closure querying the database — the pattern the
generated scaffolding already stubs as `nameAvailable`; `internal/validate`
itself stays stdlib-only and database-free.

Password fields:
- **Admin reset** — one optional field on the edit form; blank means unchanged.
- **Self-service** — current password, new password, confirm. The current
  password is verified with `verifyPassword`; a wrong one is a field error, not
  a 403.

## 8. The permission editor

On the group edit page, below the group's name and superuser flag.

Rows come from `adm.Permissions()`, aggregated by resource into `access` and
`modify` columns. `superuser = 1` makes the grid advisory — the group bypasses
every check — so the page says so rather than pretending the checkboxes matter.

`modify` implies `access` **one-way**, exactly as `permSet.Allows` has it. The
grid ticks `access` when `modify` is ticked and unticks `modify` when `access` is
unticked — and the server normalizes the same way on save. The client behaviour
is convenience; it cannot be the enforcement point.

Stale-key cleanup is **orthogonal to the superuser flag** — it runs for a
superuser group too. The stored set should always equal what the interface
showed, whether or not anything currently consults it; skipping the cleanup for
superuser groups would leave a group's stored permissions silently disagreeing
with its own edit page the moment the flag were cleared.

**Stale keys.** A group may hold keys for resources that no longer register
routes. They appear below the grid, listed, with "保存将清除以下失效权限". After
saving, the stored set equals the checkbox state — nothing invisible survives.
The alternative (silently writing unknown keys back) leaves permissions in
effect that the interface never showed.

## 9. Files

```
internal/controller/admin/user_crud.go       the user resource
internal/controller/admin/group_crud.go      the group resource + permission grid
internal/controller/admin/account.go         self-service password (exempt route)
internal/controller/admin/guardrails.go      the three rules, shared
internal/service/db/migrations/004_user_status.{up,down}.sql
frontend/pages/admin/user/{index,form}.vue
frontend/pages/admin/group/{index,form}.vue
frontend/pages/admin/account/password.vue
```

Modified: `group.go` (`findGroup` gains `status`), `auth.go` (`resolve`'s third
outcome), `user.go` (three changes that go together: `User` gains `Status`,
`findUser`'s SELECT gains the column, and `authenticate` refuses a disabled
account after the password verifies — see §4), `admin.go` (`Mount`),
`AdminShell.vue` (the user-menu item), `components.go` (the new pages are
admin-owned), README.

Pages are assembled from the existing composites — `AdminShell`, `PageHeader`,
`DataTable`, `FormField`, `ConfirmDialog`. The permission grid is the only new
markup, and it is a table of checkboxes.

The user list joins `user_groups` for the group name; the group list counts
members per group. Both are one query.

## 10. Testing

- **Each guardrail**, driven through the real engine over HTTP: deleting
  yourself, disabling yourself, changing your own group.
- **Rule 3 on all four paths** — disable, delete, move out of a superuser group,
  clear a group's superuser flag — each asserting the change was *rolled back*,
  not merely refused: the row must still be there afterwards.
- **A disabled user with a live session** is bounced to login, sees the flash,
  and cannot log back in. The test must assert the flash actually arrives —
  staging it and destroying the session are mutually exclusive, and getting that
  backwards would silently lose the only explanation the user gets.
- **Two admin requests in a row from a disabled user** each re-stage the flash
  and each bounce. That is the intended behaviour, not a bug to suppress: every
  attempt should say why it failed, and the alternative — a "already told them"
  marker in the session — is state to no purpose. The test pins it so nobody
  later mistakes the repetition for a defect.
- **`authenticate` rejects a disabled account only after the password verifies**,
  and a wrong password on a disabled account is still reported as invalid
  credentials. Asserting the order is what keeps the timing oracle closed.
- **Enabling is not subject to rules 1 and 3**, and `status` is taken from the
  request rather than toggled: submitting `status=1` twice leaves the user
  enabled.
- **The three outcomes stay distinguishable**: no group → 403, storage failure →
  500, disabled → redirect. A test per branch; conflating any two would make an
  outage or a disabled account look like the wrong thing.
- **Migration 004**: existing rows land as active, `ADD COLUMN` is applied once,
  the down migration reverses cleanly.
- **Group deletion** with members is refused and names the count; with none it
  succeeds.
- **Permission save**: stale keys are dropped, `modify` implies `access` after
  normalization even if the client sent only `modify`.
- **The permission grid's implication behaviour** — vitest, since it is new
  interactive markup.
- **The exempt account route** is reachable by a user whose group grants nothing,
  and rejects a wrong current password with a field error rather than a 403.
- **Trim**: `init --dry-run` with admin off deletes the new pages and handlers.

## 11. Out of scope

- Audit log of administrative actions.
- Last-login tracking, failed-login counting, lockout. **Superseded:** counting
  and lockout came back one spec later and now ship — see
  `2026-07-27-csrf-and-login-throttling-design.md` and migration
  `005_login_attempts`. Last-login tracking is still not done.
- Email, password reset by email, public registration.
- Bulk actions on the user list.
- Per-user permission overrides — permissions live on the group, and that stays
  the whole model.
- Session listing or remote sign-out beyond the disable path above.
