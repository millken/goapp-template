# Session Flash Messages — Design Spec

Date: 2026-07-25 · Status: Draft — pending user review · Depends on: the session component only

## 1. Goal

Carry a one-shot message across a redirect, so a POST that redirects can tell the user what
happened.

The gap is concrete. `goappctl gen admin <name>` generates CRUD that answers every write with a
303:

```go
func (ct *Controller) Create(c *inertia.Context) {
	...
	http.Redirect(c.Writer, c.Request, ct.base, http.StatusSeeOther)
}
```

A redirect ends the handler, so every prop set with `c.Set` is discarded. Generated admin CRUD
therefore has **no way to report "saved" or "delete failed"** — the one problem flash messages
exist to solve. The session service already owns the storage, the signed cookie, and the
write-through writer; flash is a thin read-once layer on top.

Note the contrast with `admin.LoginSubmit`, which **re-renders** on failure rather than redirecting
(`internal/controller/admin/handlers.go:41-46`) and so needs nothing new. That is why the gap has
gone unnoticed: it appears the moment `gen` is used.

## 2. Decisions

| Question | Decision |
|---|---|
| Scope | One-shot messages only — **no** validation-error / old-input bag |
| Validation errors | Keep re-rendering, as login already does |
| Storage | Session values, flat `string` per message, key `_flash:<kind>` |
| Who clears it | The session middleware, before `c.Next()` — read, clear, `Save` |
| Public read API | None; consumption is package-private |
| Prop | `flash?: Record<string, string>`, kind → message |
| Rendering | Inline in `AdminLayout.vue`, top of `<main>` |
| Scaffold | `admin/*` templates flash on write; `resource/*` deliberately untouched (§8) |

**Why messages only.** A form-error bag would let `Create` redirect back to the form instead of
re-rendering, but re-rendering already handles validation errors correctly in this stack: under PJAX
a render becomes a `{_ViEW_}` payload that swaps the page with the URL unchanged. The extra
mechanism buys a second way to do what one way already does.

## 3. Non-goals

- **Validation errors and old-input repopulation.** Out of scope, per §2.
- **Flashing to a visitor who has no session.** Would require `Save` on an anonymous request, which
  sets a cookie for someone who never had one. Login failure keeps re-rendering.
- **Dismiss buttons and auto-dismiss timers.** The message is already gone server-side; it
  disappears on the next navigation.
- **A global client store to avoid prop threading.** Prop threading is the existing convention
  (`adminMenu`, `adminUser`); a second state mechanism alongside it costs more than it saves.
- **Flash for the non-admin `resource` scaffold.** See §8.

## 4. Storage: why flat string keys

The two stores serialize differently. `store_db` JSON-encodes values
(`internal/service/session/store_db.go:71,85`); `store_memory` keeps native Go values. So a
`map[string]string` written into the session reads back as `map[string]string` from memory but as
`map[string]any` from the database — a divergence that only shows up in production, since the
template defaults to memory and production uses db.

The design sidesteps it: every flash is a **flat key holding a string**, and `string` survives a
JSON round-trip identically in both stores.

```go
const flashPrefix = "_flash:" // _flash:success → "Post created"
```

The `_` prefix marks the key as framework-reserved (as `_ViEW_` does in the PJAX payload) and cannot
collide with an application key or with admin's configurable `authKey`.

## 5. API surface

`Session` (`internal/service/session/store.go:10`) gains one method:

```go
// Flash stores a one-shot message under kind ("success", "error", ...). It is
// injected as the `flash` prop on the next request and removed as it is read.
// Call Save afterwards — Flash only stages the value, like Set.
Flash(kind, message string)
```

Implementation in a new `internal/service/session/flash.go`:

```go
func (s *session) Flash(kind, message string) { s.Set(flashPrefix+kind, message) }
```

Consumption is **package-private** — `takeFlash()` scans keys with `flashPrefix`, strips the prefix
into a `map[string]string`, and deletes them from the session values. No public read method: reading
is the middleware's job (§6), and exposing it would only invite a caller to consume the flash
without persisting the removal.

Writer-side usage is one line plus the `Save` the existing contract already requires:

```go
sess := ct.Session.Session(c)
sess.Flash("success", "Post created")
if _, err := sess.Save(c.Request.Context()); err != nil { slog.Error(...) }
http.Redirect(c.Writer, c.Request, ct.base, http.StatusSeeOther)
```

## 6. Middleware: read, clear, persist

Read-once requires a write: if the removal is not persisted, the message repeats forever. The write
happens in `Middleware()` **before** `c.Next()`:

```go
func (s *Service) Middleware() inertia.HandlerFunc {
	return func(c *inertia.Context) {
		sess := s.loadOrCreate(c.Request.Context(), c.Request)
		sess.w = c.Writer
		c.Set(contextKey, sess)

		// Consume any staged flash before the handler runs: the removal must be
		// persisted, and Save here lands before the body flushes.
		if flash := sess.takeFlash(); len(flash) > 0 {
			if _, err := sess.Save(c.Request.Context()); err != nil {
				// Store write failed, so the flash is still there. Don't inject
				// it: showing it now would show it again next request too.
				slog.Warn("session: persisting flash consumption failed", "err", err)
			} else {
				c.Set("flash", flash)
			}
		}
		c.Next()
	}
}
```

Four properties, each deliberate:

**The `Save` contract is unchanged.** Rejected alternative: track a dirty flag on the session and
auto-`Save` after the handler. That would give business code zero boilerplate but overturn "`Save`
is called explicitly by the caller" (`internal/service/session/impl.go:37-47`) for every consumer,
to serve one feature. Also rejected: make the reader call `Consume` + `Save` itself — an API where
forgetting a line wedges a message permanently does not belong in a template.

**Clearing before the handler runs is what makes the timing safe.** The `Set-Cookie` that `Save`
emits lands before the body flushes, so the write-through-writer hazard documented in `impl.go`
cannot occur. The accepted cost: if the handler then panics or redirects elsewhere, that message was
already consumed and is lost. The alternative — clear only after a successful render — either cannot
be done at this layer or tolerates duplicate display, and a message shown twice is more annoying
than one lost occasionally.

**A failed `Save` suppresses the prop rather than losing the message.** The store still holds the
flash, so the next request retries and displays it once the store recovers. At-most-once is a hard
guarantee.

**No flash means no extra work.** Requests without a staged flash follow exactly the current path —
no store write, no cookie. Anonymous visitors get an empty session from `loadOrCreate`, `takeFlash`
returns nothing, nothing is saved, and **no cookie is set for them**. One visible side effect: the
request that *carries* a flash also refreshes the cookie `MaxAge`, because `setCookie` runs inside
`Save` — a harmless one-off sliding renewal, recorded here so it does not read as a bug later.

## 7. Frontend contract

The prop is `flash?: Record<string, string>`. Absent means render nothing — which is also what a
session-less build produces, so no `//goappctl:` marker is needed on the frontend.

`AdminLayout.vue` renders it at the top of `<main>`, so every admin page — including generated ones —
gets it for free:

```ts
const props = defineProps<{
  menu?: MenuItem[]
  user?: unknown
  mount?: string
  loginPath?: string
  flash?: Record<string, string>
}>()

const flashClass = (kind: string) =>
  ({
    success: 'bg-green-50 text-green-800 border-green-200',
    error: 'bg-red-50 text-red-800 border-red-200',
  })[kind] ?? 'bg-gray-50 text-gray-700 border-gray-200'
```

```vue
<main class="flex-1 p-8">
  <div
    v-for="(message, kind) in flash || {}"
    :key="kind"
    class="mb-4 border rounded px-4 py-3 text-sm"
    :class="flashClass(kind)"
  >{{ message }}</div>
  <slot />
</main>
```

Pages declare the prop and pass it down, exactly as they already do for `adminMenu` / `adminUser`:

```vue
defineProps<{ /* ... */ flash?: Record<string, string> }>()
<AdminLayout :menu="adminMenu" :user="adminUser" :mount="adminMount" :login-path="loginPath" :flash="flash">
```

Extracting a `FlashMessages.vue` is left until a second layout exists; with one consumer, eight
inline lines read better than another file, and the extraction is mechanical.

## 8. Scaffold changes

`templates/admin/handler.go.tmpl` gains a local helper and one call per write:

```go
// flash stages a one-shot message for the page we are about to redirect to; the
// session middleware injects and clears it on the next request.
func (ct *Controller) flash(c *inertia.Context, kind, message string) {
	sess := ct.Session.Session(c)
	sess.Flash(kind, message)
	if _, err := sess.Save(c.Request.Context()); err != nil {
		slog.Error("flash admin [[.Package]]", "err", err)
	}
}
```

`Create` / `Update` / `Delete` call `ct.flash(c, "success", "[[.Type]] created|updated|deleted")`
before their redirect. The helper lives in the generated file rather than in
`internal/controller/admin`: that file's header already declares its methods the user's to edit, so
the message text and kind are editable in place, whereas an `admin.Admin` method would add public
API existing only for the generator plus an `*admin.Admin` field on the generated controller. Six
duplicated lines per resource is the cheaper trade.

`templates/admin/index.vue.tmpl` and `templates/admin/form.vue.tmpl` each gain the prop declaration
and `:flash="flash"`.

**`templates/resource/handler.go.tmpl` is deliberately left without flash.** Its three redirects
(lines 64, 84, 92) drop feedback the same way, but that template must compile in a session-less
build, where the `Services.Session` field is stripped by the component markers
(`internal/app/services.go:31-38`). Closing this gap needs conditional generation — a separate
problem. **This is a known, accepted gap, not an oversight.**

## 9. Testing

`internal/service/session/flash_test.go`, using the harness already in `session_test.go`
(`newTestEngine` + `installed`, handlers asserting via `c.Get`) — so the tests never depend on the
render payload shape. The db leg uses `sqldb.Open("sqlite3", ":memory:")`, as `store_test.go:104`
already does.

The first three cases run against **both** stores: the key design (§4) exists to keep memory and db
from diverging, so that invariant needs a test guarding it.

1. **Exactly once** — request A flashes and saves; request B (with the cookie) sees the `flash` prop
   with the right contents; request C does not.
2. **Multiple kinds** — `success` and `error` flashed together arrive in one map with prefixes
   stripped.
3. **No flash, no cost** — a request carrying a session that holds no flash emits no `Set-Cookie`,
   proving the middleware did not save. (Distinct from the existing
   `TestMiddleware_NoCookieWhenSessionUntouched`, which covers a request with no session at all.)
4. **Failed `Save` suppresses the prop** — with a Store stub that always errors, the prop is absent
   and the flash remains in the store for a later retry. (Stub-driven; not run per store.)

No frontend test: a `v-for` plus a class lookup table is presentation, and the table is its own
specification. Deliberate, not an omission.

## 10. Live verification

Run against `go run . serve` with a temporary `sess.Flash` in `LoginSubmit` (reverted afterwards),
because no shipped handler flashes — only generated ones do.

`POST /admin/login` with `X-Pjax` returned `{"redirect":"/admin"}`; the following
`GET /admin` payload carried the message, and the next identical request did not:

```json
{ "_ViEW_": "admin/dashboard", "adminUser": 99,
  "flash": { "success": "LIVE CHECK: signed in" }, ... }
```

So the middleware's `c.Set("flash", …)` does reach the rendered props, and delivery is read-once
against a real server — the two claims the unit tests approximate through `c.Get`.

### 10.1 The session prop leak this uncovered (fixed)

That first payload also contained `"session": {}`. Root cause: inertia's `Context.data` is both the
middleware scratchpad (`Set`/`Get`) and the props bag, which `Render` serializes wholesale —
`c.JSON(c.data)` under PJAX, `jsonMarshal(c.data, true)` into `data-page` otherwise. The middleware
stored the live session there under the key `session`, so it shipped to the browser on every render.
It marshaled to `{}` only because every field of `session` is unexported: the leak was structural and
its harmlessness accidental — one exported field would have put session contents in the page.

Fixed by moving the session to the request's `context.Context`, keyed by an unexported
`sessionCtxKey{}`, which is where request-scoped non-presentational state belongs in Go. `Session(c)`
reads it back from `c.Request.Context()`. Props now carry only what the page renders.

Rejected alternatives: deleting the key before each render (every render path would have to remember,
and forgetting is silent); teaching inertia to skip reserved key prefixes (`_ViEW_` is deliberately a
prop, so a `_` rule contradicts itself, and it would put the template back on an unreleased library
change).

Re-verified live on the fixed code: `GET /` and `GET /admin/login` payloads contain only their real
props, and the authenticated dashboard payload delivers `flash` once with no `session` key.

`internal/service/session/session_test.go` grows `TestMiddleware_SessionIsNotAProp`, which fails if
anything in the props bag is a `*session` or sits under the key `session`.

## 11. Files touched

| File | Change |
|---|---|
| `internal/service/session/flash.go` | New: `flashPrefix`, `Flash`, `takeFlash` |
| `internal/service/session/store.go` | `Session` interface gains `Flash(kind, message string)` |
| `internal/service/session/session.go` | `Middleware()` gains read-clear-persist; session moves to the request context (§10.1) |
| `internal/service/session/flash_test.go` | New |
| `internal/service/session/session_test.go` | `TestMiddleware_SessionIsNotAProp` (§10.1) |
| `frontend/src/components/AdminLayout.vue` | `flash` prop, `flashClass`, render block |
| `frontend/pages/admin/dashboard.vue` | Declare and pass `flash` |
| `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` | `flash` helper + 3 calls |
| `cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl` | Pass `flash` through |
| `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl` | Pass `flash` through |
