# Request Validation — Design Spec

Date: 2026-07-25 · Status: Validated — approved for planning · Depends on: nothing (a leaf package)

## 1. Goal

Give handlers a way to validate submitted input and re-render the form with
per-field errors and repopulated values — the one capability the flash spec
(§2) explicitly deferred.

The gap is the mirror image of flash. `goappctl gen resource|admin <name>`
generates CRUD that writes then redirects; today the only feedback it can offer
is a success flash. When input is bad the handler has no structured way to say
*which* fields failed. Login already does the right thing by re-rendering on
failure (`internal/controller/admin/handlers.go`), but ad hoc, with a single
generic `error` prop. This spec makes that pattern first-class: a small
validator that produces an `errors` prop for the re-render, in the same `c.Set`
shape the templates already use for `flash`, `adminMenu`, `adminUser`.

## 2. Decisions

| Question | Decision |
|---|---|
| Style | **Programmatic, rules-as-values** — no struct-tag / reflection engine |
| Rule contract | `type Rule func(value string) error` — nil passes; the error's text is the user-facing message |
| On failure | **Same-request re-render.** No cross-redirect error bag |
| Relationship to flash | Success across a redirect = flash; **validation errors never enter the session** |
| Relationship to login's `error` | `error` (singular) = one form-level message (bad credentials); `errors` (plural) = per-field map. They coexist |
| Layer | A leaf package `internal/validate`, **not** a component — no `Start/Stop`, no `Services` field, no `//goappctl:` marker |
| Dependencies | **stdlib only.** No `sqldb`, no third-party validator |
| DB-backed rules | **Not in the package.** Uniqueness is an ordinary `Rule` closure written in the handler (§6) |
| Custom messages | One combinator, `Msg(rule, text)` — not a `msg ...string` on every rule |
| Repopulation | **From the bound model `item`**, which the form already renders. No `old` prop |
| Props | `errors?: Record<field, string>` — that is the only new prop |
| Frontend | Inline in the form, like flash — no `FormField.vue` until a second form exists |

**Why programmatic, not tags.** Tag-driven validation (`validate:"required,email"`)
looks like it fits the generated models, but it needs reflection, and the moment
a rule consults the database the tag must hardcode table/column while the engine
obtains a `*sqldb.DB` mid-reflection — magic that clashes with how this stack is
built everywhere else (session, store, `Services` are all explicit). This rules
out `go-playground/validator` despite its popularity.

**Why rules-as-values, not a fluent chain.** An earlier draft used method
chaining (`v.Field("n", val).Required().Min(2)`). Making a rule a *value* that
returns `error` is strictly smaller and more extensible: a user-defined rule is
any `func(string) error`, so no `.Func` escape hatch is needed; a custom message
is one combinator instead of a variadic parameter on eight signatures; and
DB-backed checks are plain closures, which is what lets the package stay
stdlib-only. This shape is borrowed from `ozzo-validation`; the library itself is
**not** adopted — it has been unmaintained for years, and what remains after
borrowing the shape is ~100 lines.

**Why re-render, not PRG.** The flash spec already fixed the split: success
messages cross a redirect (flash); validation errors do not. Under PJAX a
re-render is a `{_ViEW_}` payload that swaps the page in place with the URL
unchanged — which is already the PRG outcome. A non-PJAX request re-renders the
form page natively. So an error bag carried through the session would be a second
mechanism doing what one already does.

**Why no `old` prop.** Rails does not carry submitted values in a side channel;
it re-renders from the model instance it just failed to save, which already holds
them. Both generated form templates already bind `:value="item.name"` for the
edit case (`templates/{admin,resource}/form.vue.tmpl`), so a handler that binds
the form into `item` *before* validating gets repopulation for free, on the code
path that already exists. This also makes sensitive fields safe by default: a
password never enters the model struct, so it can never be echoed back — the
earlier `old` + `.Hide()` design was safe only if you remembered `.Hide()`.

## 3. Non-goals

- **Struct-tag / reflection-driven validation.** Rules are values.
- **Database-aware rules in the package.** No `Unique`/`UniqueExcept`; §6 shows
  the closure. This is what keeps `internal/validate` free of `sqldb` and
  therefore unconditionally compilable in every component combination.
- **Cross-redirect (PRG) error bag.** Re-render covers it; flash covers success.
- **Automatic form→struct binding.** Handlers bind fields explicitly
  (`c.PostForm`). A `Bind` helper belongs to the data layer, not here.
- **i18n / message catalogs.** Default English; `Msg` overrides any single rule.
  Localization is a separate, cross-cutting spec.
- **Multiple messages per field.** One message per field (§5).
- **Async rules, file-upload validation.**

## 4. API surface

```go
// internal/validate
package validate

// Rule checks one submitted value. A nil error means the value passed; a
// non-nil error's message is shown to the user verbatim, so it should read as a
// predicate about the field ("can't be blank"), not a sentence.
type Rule func(value string) error

// Validator collects at most one error message per field for one submission.
// Not safe for concurrent use; one per request.
type Validator struct {
    errs map[string]string // field → message; allocated on first failure
}

func New() *Validator

// Field runs rules against value in order and stops at the first failure,
// recording its message under name. If name already has an error, no rule runs
// at all — so calling Field twice for one field cannot overwrite its message.
func (v *Validator) Field(name, value string, rules ...Rule) *Validator

// Check records msg for field when ok is false. It is the escape hatch for
// cross-field assertions (password confirmation), and unlike Field it can
// report on a field that was never passed to Field.
func (v *Validator) Check(ok bool, field, msg string) *Validator

func (v *Validator) OK() bool                  // no errors recorded
func (v *Validator) Errors() map[string]string // field → message; populates the `errors` prop
```

Rules. The parameterless ones are plain `Rule` values, not factories, so they
read as `validate.Required` rather than `validate.Required()`:

```go
var (
    Required Rule // non-empty after TrimSpace
    Email    Rule // net/mail parse
    Int      Rule // strconv.ParseInt, base 10
)

func MinLen(n int) Rule                  // rune count >= n
func MaxLen(n int) Rule                  // rune count <= n
func Match(re *regexp.Regexp) Rule
func In(allowed ...string) Rule

// Msg wraps a rule to replace its message. It is the only way to customize
// text, and it works on user-defined rules too.
func Msg(r Rule, text string) Rule
```

Named `MinLen`/`MaxLen` rather than `Min`/`Max` because the package also has
`Int`, which would make `Min(1)` read as a numeric lower bound. There is no
`Between`: `MinLen(2), MaxLen(80)` composes.

Imports: `strings`, `regexp`, `net/mail`, `strconv`, `unicode/utf8`, `fmt`,
`errors`. Nothing else.

## 5. Rule semantics

- **First failure wins.** `Field` stops at the first rule that fails, and a field
  that already has an error is skipped. One message per field — the common UX,
  and it makes an expensive rule (a DB query) automatically lazy: it never runs
  when `Required` already failed, so no query is issued for empty input.
- **Every rule except `Required` treats an empty value as passing.** This is what
  makes an optional field just `v.Field("email", email, validate.Email)` — blank
  is accepted, non-blank must parse — with no `if email != ""` in the handler.
  Emptiness here means `strings.TrimSpace(value) == ""`, the same test `Required`
  uses. A rule written by a user should follow the convention; nothing enforces it.
- **`Required` trims**, so a whitespace-only string fails.
- **Length counts runes**, not bytes.
- **`Check` also respects first-failure-wins** — it is a no-op when the field
  already has an error.
- **Default messages** are short and field-agnostic, because the field name is
  conveyed by the prop key and rendered next to the input:

  | Rule | Message |
  |---|---|
  | `Required` | `can't be blank` |
  | `Email` | `must be a valid email` |
  | `Int` | `must be a number` |
  | `MinLen(n)` | `must be at least %d characters` |
  | `MaxLen(n)` | `must be at most %d characters` |
  | `In(a…)` | `must be one of: %s` (comma-joined) |
  | `Match(re)` | `is not valid` |

  `Match` has no better default — a regexp has no human-readable form — so it is
  the one rule expected to be wrapped in `Msg` in practice.
- **`Errors()` returns the live map**, not a copy — it is per-request and goes
  straight into `c.Set`. It is nil until the first failure, so handlers set the
  prop only when `!v.OK()` (§7).

## 6. Usage in a handler

Materialized form of a generated admin `Create` (the generator substitutes
`[[.Type]]`, `[[.Table]]`, `[[.ViewDir]]`). Note the order: **bind, then
validate, then re-render the same `item`.**

```go
func (ct *Controller) Create(c *inertia.Context) {
	ctx := c.Request.Context()
	item := [[.Type]]{Name: c.PostForm("name")}

	v := validate.New()
	v.Field("name", item.Name, validate.Required, validate.MaxLen(200), ct.nameAvailable(ctx, 0))

	if !v.OK() {
		c.Set("errors", v.Errors())
		c.Set("item", item) // repopulates the form — no `old` prop
		c.Set("basePath", ct.base)
		if err := c.Render("admin/[[.ViewDir]]/form"); err != nil {
			slog.Error("render admin [[.Package]] form", "err", err)
		}
		return
	}

	// TODO: _ = ct.DB.Table("[[.Table]]").Insert(&item)
	ct.flash(c, "success", "[[.Type]] created")
	http.Redirect(c.Writer, c.Request, ct.base, http.StatusSeeOther)
}
```

A DB-backed rule is an ordinary closure. It is a method returning a `Rule` so
`Update` can reuse it while excluding the row's own id — the job the deleted
`UniqueExcept` used to do:

```go
// nameAvailable reports names not already taken by another row. exceptID skips
// one row, so Update does not collide with itself; pass 0 from Create.
//
// A query failure degrades to a user-facing message rather than a 500: the
// error is logged here, and the form comes back with something actionable.
func (ct *Controller) nameAvailable(ctx context.Context, exceptID int64) validate.Rule {
	return func(name string) error {
		taken, err := ct.nameTaken(ctx, name, exceptID)
		if err != nil {
			slog.Error("check [[.Table]] name", "err", err)
			return errors.New("could not be verified, please try again")
		}
		if taken {
			return errors.New("is already taken")
		}
		return nil
	}
}
```

`nameTaken` is a generated TODO stub on the controller — a plain method issuing
one `SELECT`. **No `model.Exists` helper is introduced**: there is exactly one
consumer, so extracting it now would be premature.

`Update` is the same with `ct.nameAvailable(ctx, id)`. `Delete` validates only
the path id and skips the validator entirely.

The public (`gen resource`) variant is identical except that it renders
`"[[.ViewDir]]/form"`, sets `viewDir` instead of `basePath`, and has no flash —
`templates/resource/handler.go.tmpl` never touches the session, because a public
resource must compile in a session-less build. **Validation works there
regardless**, which is the practical payoff of keeping errors out of the
session: a build with neither `session` nor `db` still gets full form validation.

## 7. Props & frontend

`errors?: Record<string, string>` is injected via `c.Set` exactly like `flash`,
and is the only prop this spec adds. It is absent on a clean request — handlers
set it only when `!v.OK()` — so the success path pays nothing.

Both form templates gain the prop and render it under the input:

```vue
defineProps<{
  item: { id: number; name: string }
  errors?: Record<string, string>
  // …existing props…
}>()
```

```vue
<input
  name="name"
  :value="item.name"
  class="mt-1 block w-full border rounded px-2 py-1"
  :class="errors?.name ? 'border-red-500' : ''"
/>
<p v-if="errors?.name" class="mt-1 text-sm text-red-600">{{ errors.name }}</p>
```

Kept inline; a `FormField.vue` is deferred until a second form exists (the same
call the flash spec made for `FlashMessages.vue`).

**Three message channels, not overlapping:** `error` (login's singular,
form-level "invalid credentials"), `errors` (per-field, this spec), `flash`
(cross-redirect success). Login is unchanged; if it ever wants per-field errors
it can adopt `errors` without displacing `error`.

## 8. Why a leaf package, not a component

`validate` has no lifecycle, owns no resources, and wires into nothing at
startup — so it is not a `goappctl` component and carries no marker. Because it
imports only stdlib, it compiles in every component combination with no
qualification: nothing to strip, no nil field to guard, no dependency on `db` or
`session`. This is a direct consequence of §2's decision to keep DB rules out of
the package.

## 9. Testing

`internal/validate/validate_test.go` — the whole package is pure functions of
input, so no harness is needed:

- **Each rule**, table-driven over pass/fail cases: `Required` rejecting
  whitespace-only; `MinLen`/`MaxLen` counting runes (a multi-byte string must not
  be measured in bytes); `Email`, `Int`, `Match`, `In`.
- **The empty-value convention**: every rule except `Required` passes on `""`
  and on `"   "`.
- **`Msg`** replaces the message of both a built-in and a user-defined rule.
- **`Validator`**: first-failure-wins (a field's second failing rule does not
  overwrite the first); a rule after a failure is *not invoked* (assert via a
  counting closure — this is what makes DB rules lazy); `Check` records, and is a
  no-op on an already-failed field; `OK()`/`Errors()` on a fresh validator.

Generated-code coverage needs no new mechanism: the existing
`TestResource_OutputCompiles` / `TestAdmin_OutputCompiles` in
`cmd/goappctl/internal/scaffold/build_test.go` render into the real module tree
and run `go build`, so a validate block that does not compile fails the suite.
Add one assertion in the `*_test.go` content tests that the rendered handler
contains the validate block, matching the existing `TestResource_TemplateContent`
style.

No frontend test: the form binding is presentation.

## 10. Files touched

| File | Change |
|---|---|
| `internal/validate/validate.go` | New: `Rule`, `Validator`, rules, `Msg` |
| `internal/validate/validate_test.go` | New |
| `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` | `Create`/`Update` bind the form, validate, re-render on failure; add `nameAvailable` + `nameTaken` stubs |
| `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl` | Declare `errors?`; render per-field message + error border |
| `cmd/goappctl/internal/scaffold/templates/resource/handler.go.tmpl` | Same as the admin handler, minus flash |
| `cmd/goappctl/internal/scaffold/templates/resource/form.vue.tmpl` | Same as the admin form |
| `cmd/goappctl/internal/scaffold/{admin,resource}_test.go` | Assert the validate block is rendered |
| `README.md` | One line under the generator section: generated write handlers validate and re-render |

`internal/controller/admin/handlers.go` (login) is **not** changed — its
form-level `error` prop is a different channel (§7).

`templates/{admin,resource}/index.vue.tmpl` are **not** changed: `errors` only
ever reaches a form, never the list page.
