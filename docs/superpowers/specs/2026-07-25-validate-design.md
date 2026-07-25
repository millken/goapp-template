# Request Validation — Design Spec

Date: 2026-07-25 · Status: Draft — pending user review · Depends on: nothing (a leaf package)

## 1. Goal

Give handlers a way to validate submitted input and re-render the form with
per-field errors and repopulated values — the one capability the flash spec
(§2) explicitly deferred.

The gap is the mirror image of flash. `goappctl gen admin <name>` generates CRUD
that writes then redirects; today the only feedback it can offer is a success
flash. When input is bad the handler has no structured way to say *which* fields
failed and *what the user typed* — it must either reject blindly or accept
garbage. Login already does the right thing by re-rendering on failure
(`internal/controller/admin/handlers.go`), but ad hoc, with a single generic
`error` prop. This spec makes that pattern first-class: a small validator that
produces `errors` and `old` props for the re-render, in the same `c.Set` shape
the template already uses for `flash`, `adminMenu`, `adminUser`.

## 2. Decisions

| Question | Decision |
|---|---|
| Style | **Programmatic, chainable** — no struct-tag / reflection engine in v1 |
| On failure | **Same-request re-render.** No cross-redirect error bag |
| Relationship to flash | Success across a redirect = flash; **validation errors never enter the session** |
| Relationship to login's `error` | `error` (singular) = one form-level message (bad credentials); `errors` (plural) = per-field map. They coexist |
| Layer | A leaf package `internal/validate`, **not** a component — no `Start/Stop`, no `Services` field, no `//goappctl:` marker |
| DB-backed rules | `Unique` / `UniqueExcept` take `*sqldb.DB`; nil ⇒ skip (db-less builds) |
| Props | `errors?: Record<field, string>`, `old?: Record<field, string>` |
| Old input | Auto-staged by `Field`; `.Hide()` excludes sensitive fields |
| Frontend | Inline in the form, like flash — no `FormField.vue` until a second form exists |

**Why programmatic, not tags.** Tag-driven validation (`validate:"required,email"`)
looks like it fits the generated models, but the moment a rule needs the database
(unique) the tag must hardcode table/column and the engine must obtain a `*sqldb.DB`
during reflection — magic that clashes with how this stack is built everywhere else
(session, store, Services are all explicit). A programmatic validator is ~150 lines,
no reflection, and DB rules are ordinary method calls. A tag/reflection helper can
land later as an *optional* shim over the same primitives without redesigning v1.

**Why re-render, not PRG.** The flash spec already fixed the split: success messages
cross a redirect (flash); validation errors do not. Under PJAX a re-render is a
`{_ViEW_}` payload that swaps the page in place with the URL unchanged — which is
already the PRG outcome. A non-PJAX request re-renders the form page natively. So
an error bag carried through the session would be a second mechanism doing what one
already does.

## 3. Non-goals

- **Struct-tag / reflection-driven validation.** Programmatic first; a `Reflect`
  helper is a later opt-in, not a v1 concern.
- **Cross-redirect (PRG) error bag.** Re-render covers it; flash covers success.
- **Automatic form→struct binding.** Handlers bind fields explicitly
  (`c.PostForm`). A `Bind` helper belongs to the data layer (model conventions),
  not here.
- **i18n / message catalogs.** Default English; every rule accepts an optional
  custom message. Localization is a separate, cross-cutting spec.
- **Async or externally-driven rules** beyond `.Func`. File-upload validation is
  out of scope.

## 4. API surface

```go
// internal/validate
package validate

// Validator collects per-field errors and old values for one submission.
// Not safe for concurrent use; one per request.
type Validator struct {
    errs  map[string]string // field → message
    old   map[string]string // field → submitted value (for repopulation)
    field string            // current chain target
    val   string
}

func New() *Validator

// Field starts a rule chain. label is the errors/old key (usually the form
// field name). The value is staged into old for repopulation; call .Hide()
// to keep a sensitive field (password) out of old.
func (v *Validator) Field(label, value string) *Validator
```

Pure rules — each chainable, each accepting an optional custom message, and each
**stopping at the first failure for its field** (one message per field, the common
UX):

```go
.Required(msg ...string) *Validator                  // non-empty after TrimSpace
.Min(n int) *Validator                               // rune length ≥ n
.Max(n int) *Validator
.Between(a, b int) *Validator
.Email() *Validator                                  // net/mail parse
.Match(re *regexp.Regexp) *Validator
.In(allowed ...string) *Validator
.Int() *Validator                                    // parses as a base-10 integer
.Func(fn func(value string) bool, msg string) *Validator
.Hide() *Validator                                   // drop the current field from old
```

DB-backed rules — chainable, reuse the current field; nil `db` is a no-op (so a
db-less build simply doesn't call them). The `table`/`column` identifiers are
validated against `^[A-Za-z_]\w*$` and skipped with a warning if not safe to
interpolate (the value itself is always parameterized):

```go
.Unique(ctx context.Context, db *sqldb.DB, table, column string) *Validator
.UniqueExcept(ctx context.Context, db *sqldb.DB, table, column string, exceptID int64) *Validator
```

Escape hatch for cross-field assertions:

```go
.Check(ok bool, field, msg string) *Validator
```

Terminals:

```go
.OK() bool                    // len(errs) == 0
.Errors() map[string]string   // field → message; populate the `errors` prop
.Values() map[string]string   // field → submitted value; populate the `old` prop
```

## 5. Rule semantics

- **First failure wins.** Once a field has an entry in `errs`, later rules on the
  same field are no-ops. One message per field.
- **Required trims.** `Required` treats a whitespace-only string as empty, so
  `"   "` fails.
- **Length counts runes**, not bytes — correct for any input.
- **Default messages** are short and field-agnostic (`"can't be blank"`,
  `"must be a valid email"`, `"must be at least %d characters"`,
  `"is already taken"`), because the field name is conveyed by the prop key and
  rendered alongside the input. Any rule takes an optional final string to
  override.
- **`old` is authoritative for repopulation**; it holds exactly the fields passed
  to `Field`, minus any `.Hide()`.

## 6. Usage in a handler

Materialized form of a generated `Create` (the generator substitutes `[[.Type]]`,
`[[.Table]]`, `[[.ViewDir]]`):

```go
func (ct *Controller) Create(c *inertia.Context) {
	ctx := c.Request.Context()
	name := c.PostForm("name")
	email := c.PostForm("email")

	v := validate.New()
	v.Field("name", name).Required().Min(2).Max(80)
	v.Field("email", email).Required().Email().
		Unique(ctx, ct.DB, "posts", "email")
	v.Field("password", c.PostForm("password")).Required().Min(8).Hide()
	v.Check(name != email, "name", "name must differ from email")

	if !v.OK() {
		c.Set("errors", v.Errors())
		c.Set("old", v.Values())
		ct.renderForm(c, "posts/form") // existing re-render path
		return
	}
	// …persist…
	ct.flash(c, "success", "Post created") // from the flash spec
	http.Redirect(c.Writer, c.Request, ct.base, http.StatusSeeOther)
}
```

`Update` swaps `.Unique(...)` for `.UniqueExcept(ctx, ct.DB, "posts", "email", item.ID)`
so the row's own value is ignored. The three write handlers in
`cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` (Create/Update/Delete)
gain this block ahead of their existing redirect; `Delete` typically validates only
the id and may skip the validator.

## 7. Props & frontend

- `errors?: Record<string, string>` and `old?: Record<string, string>`, injected via
  `c.Set` exactly like `flash`. Absent on a clean request — the handler sets them
  only when `!v.OK()`, so there is no prop-threading cost for the success path.
- The form binds each input to `old?.<field>` and shows `errors?.<field>` beneath it.
  Kept inline; a `FormField.vue` is deferred until a second form exists (same call as
  the flash spec's `FlashMessages.vue`).
- **Three message channels, not overlapping:** `error` (login's singular,
  form-level "invalid credentials"), `errors` (per-field, this spec), `flash`
  (cross-redirect success). Login is unchanged; if it ever wants per-field errors,
  it can adopt `errors` without displacing `error`.

## 8. Why a leaf package, not a component

`validate` has no lifecycle, owns no resources, and wires into nothing at startup —
so it is not a `goappctl` component and carries no marker. It imports only
`sqldb` (already a core `go.mod` dep — the accepted wart from the goappctl spec),
so it compiles in every component combination. A handler in a build without `db`
simply never calls `Unique`, because `ct.DB` is nil; the method exists but is inert.
This keeps the package unconditionally present without touching the component model.

## 9. Testing

`internal/validate/validate_test.go`:

- **Pure rules** are table-driven over each rule's pass/fail cases, covering custom
  messages, first-failure-wins, rune-length, and `Hide()` keeping a field out of
  `old`. No harness needed — these are pure functions of input.
- **DB rules** use `sqldb.Open("sqlite3", ":memory:")` (as `store_test.go` does):
  `Unique` flags a taken column, passes when free; `UniqueExcept` ignores the given
  id; nil `db` is a skipped no-op; a non-safe identifier is skipped with a warning.
- **One integration case** (on the existing `newTestEngine` pattern, or the future
  `apptest` helper): a failed `Create` injects `errors` and `old` and does **not**
  inject `flash` (proving the two channels are independent).

No frontend test: the form binding is presentation.

## 10. Files touched

| File | Change |
|---|---|
| `internal/validate/validate.go` | New: `Validator`, rules, terminals |
| `internal/validate/validate_test.go` | New |
| `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` | Create/Update gain the validate block |
| `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl` | Declare `errors?`/`old?`; bind + render |
| `cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl` | Thread `errors`/`old` through (as flash is) |

`internal/controller/admin/handlers.go` (login) is **not** changed.

## 11. Open questions

- **DB rules: chained `.Unique(ctx, db, table, column)` vs a standalone
  `v.Unique(...)`.** This spec picks chained, so the error key is the current
  field and the rule reads top-to-bottom with the rest of the field's checks; the
  cost is that the chain carries `ctx`/`db`. The standalone form decouples it but
  repeats the value and forces an explicit field argument. To confirm.
- **`old` staging: auto via `Field` + `.Hide()`, vs explicit `v.Old(label, value)`.**
  This spec picks auto-staging because repopulating text inputs is the common case
  and passwords are the exception worth a single `.Hide()`. Explicit `Old` doubles
  every field declaration. To confirm.
