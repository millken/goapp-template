# PJAX Navigation — Design Spec

Date: 2026-07-25 · Status: Draft — pending user review · Depends on: an unreleased inertia API (§6)

## 1. Goal

Make same-origin navigation swap page content instead of reloading the document — GitHub-style:
the URL changes, back and forward work, a progress bar appears, and nothing flickers.

The template already contains a PJAX client (`src/inertia/pjax-loader.ts`) and the server already
answers `X-Pjax: true` with JSON. Neither is reachable: **nothing imports `InertiaLink`**, so the
click handler is never bound and the `popstate` listener installed by `boot()` can never fire. This
spec wires it up, fixes the defects that went unnoticed because the code was dead, and covers the
admin flow end to end (login → dashboard → menu → logout).

## 2. Decisions

| Question | Decision |
|---|---|
| Scope | Site-wide, on by default — document-level event delegation |
| Interception | Both `<a>` clicks and `<form>` submits |
| Server redirects | Soft: `{redirect: "/path"}` JSON under PJAX, real 302 otherwise |
| Where redirect logic lives | In the inertia library (`c.Redirect`), not the template |
| Perceived behavior | Top progress bar, scroll restoration, concurrent-request cancellation |
| `history.state` | `{pjax: true, scrollY}` only — **no cached props**; popstate re-fetches |
| `InertiaLink` / `Link.vue` | Deleted — delegation makes it redundant |
| Opt-out | `data-no-pjax` on the element or any ancestor |
| Frontend tests | vitest + happy-dom (devDependencies) |

## 3. Non-goals

- **Focus management and `aria-live` announcements.** Explicitly out of scope. Known gap: after a
  swap, focus stays in the old context, so keyboard and screen-reader users get a worse experience
  than a native navigation. Record it; do not pretend otherwise.
- **Hover prefetching.** An optimization, not part of the baseline.
- **Upload progress** for multipart forms (native submits have none either).
- The `polyfills.ts` `Buffer` shim defects — unrelated to PJAX.

## 4. Client architecture

Replace the single `pjax-loader.ts` with focused modules. The current file is where the frontend's
bug density is highest, and the new work (cancellation, scroll, four-way history semantics) is
timing-dependent — it needs to be testable in isolation.

```
src/inertia/pjax/
  index.ts       enablePjax() — the only public entry; boot() calls it
  navigate.ts    visit(url, opts) — fetch → classify → mount → write history
  intercept.ts   document-level click/submit delegation + shouldIntercept predicates
  progress.ts    top progress bar
  scroll.ts      save/restore scroll offsets
```

Deleted: `src/inertia/pjax-loader.ts`, `src/inertia/Link.vue`, and the `InertiaLink` export from
`src/inertia/index.ts`.

**Deleting `Link.vue` also removes an SSR hack.** `pjax-loader.ts` opens with
`const win: Window = typeof window !== 'undefined' ? window : ({} as Window)`, and its comment says
why: `Link.vue` imports the module, so any page using `<InertiaLink>` drags PJAX into the SSR
bundle, where `window` does not exist. With the component gone, `boot.ts` is the only importer and
it runs only from the client entry (`main.ts`), so the guard can go.

### 4.1 Navigation flow

Every path funnels through one function, `visit(url, {method, body, trigger})`, where `trigger` is
`'link' | 'form' | 'popstate' | 'boot'`. The trigger says what happened; §4.2 turns it plus the
response into a history operation. `visit` is never told which history call to make:

```
click <a>  ─┐
submit form─┼→ shouldIntercept? → scroll.save() → visit()
popstate   ─┘                                        │
                                                     ▼
             abort any in-flight request → progress.start() [100ms delay]
                                                     │
                          fetch(url, {X-Pjax: true, signal})
                                                     │
                   ┌─────────────┬───────────────────┴────────┐
                !response.ok  {redirect}                   {_ViEW_}
                   │             │                            │
            hard navigate   visit(target)              mountView + history
```

- **`!response.ok`** — fall back to a full navigation (`location.href = url`) so the user sees the
  server's real error page rather than a silently dead click.
- **Aborted** — silent. Cancellation is deliberate, not an error.
- **No `_ViEW_` in the payload** — treat as a protocol violation: log and hard-navigate.
- The progress bar waits 100ms before appearing, so fast navigations do not flash it.

### 4.2 History semantics

The existing code always calls `pushState`, which builds a wrong history stack.

**The history mode is decided by the outcome, not by the caller.** An earlier draft had the caller
pass a mode that a redirect then "inherited"; that cannot work. A form submit must pick its mode
before the response exists, and neither choice survives both outcomes — `push` wrongly duplicates
the URL on a failed login, `replace` wrongly swallows the entry on a successful one. So `visit()`
takes *what triggered it* (`'link' | 'form' | 'popstate' | 'boot'`), and chooses the history
operation once the response is classified:

| Trigger | Outcome | Action |
|---|---|---|
| `boot` | initial page | `replaceState` — seeds the first entry |
| `link` | rendered | `pushState` |
| `link` | redirect to a different URL | `pushState` the target |
| `link` | redirect to the same URL | `replaceState` |
| `form` | rendered (failed login re-renders `/admin/login`) | `replaceState` — you submitted *from* this URL |
| `form` | redirect to a different URL (successful login) | `pushState` the target, matching native POST+302 |
| `form` | redirect to the same URL | `replaceState` |
| `popstate` | rendered | none — the entry already exists |
| `popstate` | redirect | `replaceState` the target |

The `popstate` + redirect row is not a detail: it is the one path §6.2 and §7's checklist depend on
(back to `/admin/login` while logged in → server redirects to the dashboard). Writing nothing there
would leave the URL at `/admin/login` showing the dashboard — exactly the URL/content mismatch that
bug 4 is about. `replaceState` rather than `pushState` because the user pressed Back; they should
not gain a forward entry for a page they never chose.

Two invariants fall out, and both are worth asserting in tests: after any `visit` settles, the
address bar equals the URL whose content is mounted; and no navigation ever leaves two consecutive
identical entries.

A redirect chain is followed at most 5 times before falling back to a hard navigation, so a
server-side redirect loop cannot hang the client.

The `boot` row fixes the broken back button: nothing in the codebase calls `replaceState` today, so
the initial entry's `state` is `null` and `onPopState` returns on its first line. Back after a PJAX
navigation silently reverts the URL while leaving the previous DOM mounted.

Going back to `/admin/login` after a successful login is accepted behavior — it matches what the
browser does natively. The server handles that case (§6.2).

### 4.3 Why `history.state` holds no props

Caching page props in `history.state` (what the current code does, and the obvious design) is
rejected:

| | Cached props | Marker + scroll only |
|---|---|---|
| `history.state` | `{pjaxUrl, pjaxData: {…all props}}` | `{pjax: true, scrollY}` |
| On back | restore from cache, no request | re-fetch, full server logic runs |
| Back to login while logged in | shows the login page | server redirects to the dashboard |
| Freshness | stale snapshot (permissions, list data) | always current |
| Safari ~2MB state cap | large pages overflow; the throw is swallowed and turns into a full navigation *after* the view already mounted | a few dozen bytes |
| Back latency | none | one JSON round trip |

Re-fetching is what the browser does natively, so this is not a regression — it stops PJAX from
being *worse* than a normal navigation. It also removes the Safari overflow defect.

### 4.4 Interception rules

Delegation binds on the **bubble** phase (the current code uses `capture: true`), so a page's own
handler can `preventDefault()` or `stopPropagation()` to opt out.

Do not intercept when:

| Condition | Reason |
|---|---|
| `e.button !== 0`, or Ctrl / Cmd / Shift / Alt held | **Fixes a live bug**: today `preventDefault()` is unconditional, so Cmd-click cannot open a new tab |
| Cross-origin, `download` attribute, or `target` pointing at another window | Not a navigation in this window |
| Hash-only `href` | Let the browser jump to the anchor |
| `data-no-pjax` on the element or an ancestor | Explicit opt-out |
| Form `method` is neither GET nor POST | Native forms do not support the others either |
| The event was already default-prevented | Someone else handled it |

Multipart forms **are** intercepted: `FormData` + `fetch` handles them correctly as long as
`Content-Type` is left unset. GET forms are serialized into the query string and visited as a GET.

Progressive enhancement is automatic: `href` and `action`/`method` are untouched, so the site works
with JavaScript disabled or broken.

## 5. Bugs this fixes

1. **PJAX is entirely unreachable.** `InertiaLink` is exported but imported by nothing — not the
   pages, not `AdminLayout` (plain `<a>`), not the scaffold templates.
2. **Back button is a no-op.** No `replaceState` seeding (§4.2).
3. **Modifier-clicks are swallowed.** No button/modifier check (§4.4).
4. **Redirects mount the wrong URL.** The server sends a real 302; `fetch` follows it transparently
   and re-sends `X-Pjax`, so the response is the *target* page's JSON — but the code pushes the
   *original* `el.href`. The address bar and the content disagree. Soft redirects remove the
   ambiguity.
5. **Safari history-state overflow** (§4.3).
6. **Cache-busting hack.** The client appends `_t=Date.now()` because one URL serves both HTML and
   JSON. `Vary: X-Pjax` (§6.1) is the correct fix; the hack goes.
7. **`Content-Type: application/json` on a GET.** `pjax-loader.ts` sets it on every request, but a
   GET has no body, so the header describes something that does not exist. Harmless today — it is a
   same-origin request, and same-origin requests are never preflighted — but it becomes a real
   preflight trigger the moment anything goes cross-origin. The rewrite sends `Accept:
   application/json` instead, which is what was actually meant. `X-Requested-With: XMLHttpRequest`
   also goes: the server reads only `X-Pjax`, so it is noise.

### 5.1 Request headers

One spelling, stated once so implementers do not have to guess which one the server matches:
**`X-Pjax: true`**. The existing client sends `X-PJAX` and the server compares against `X-Pjax`;
both work, because HTTP header names are case-insensitive and Go canonicalizes `X-PJAX` to `X-Pjax`
before comparison. The new modules use `X-Pjax` throughout for consistency with the server, and the
server must keep matching case-insensitively (Go's `Header.Get` does this for free).

## 6. Server changes

### 6.1 In the inertia library (`../inertia`)

```go
// Redirect sends {redirect: location} as JSON to a PJAX request, and a normal
// 302 otherwise, so callers do not branch on the request type.
func (c *Context) Redirect(location string)
```

Both `Render` and `Redirect` must also set **`Vary: X-Pjax`**: one URL returns HTML or JSON
depending on a request header, and without `Vary` an intermediate cache or bfcache can serve JSON
to a plain navigation.

**Release process** — the template may only use *released* dependency APIs, because `goappctl init`
deletes `go.work` and a generated project has nothing to fall back on (see the goappctl spec, §11).
So: write and test the inertia change, have the maintainer tag and push it, then bump `go.mod` here.
Do not merge the template side before the tag exists.

### 6.2 In the template

- `internal/controller/admin/auth.go`: drop the local `redirectTo` helper in favor of `c.Redirect`.
- `internal/controller/admin/handlers.go`: `LoginForm` redirects to the admin mount when the session
  is already authenticated. Purely server-side — the client stays free of auth logic, and
  `login.vue`'s "this page needs no client-side auth logic" comment stays true. It applies to *any*
  GET of the login page; the back-navigation case works only because §4.3 makes popstate re-fetch
  instead of restoring a cached snapshot.

No client code knows anything about login; it handles `{redirect}` generically.

## 7. Testing

The frontend currently has **zero tests and no runner**. Add `vitest` + `happy-dom` as
devDependencies. They never reach production: Vite bundles only what is reachable from the entries
(`main.ts`, `ssr-esm-render.ts`), test files are neither entries nor imported, and `make build-prod`
embeds only `frontend/dist`. A build-output assertion pins this.

| Module | Coverage |
|---|---|
| `intercept.ts` | Every row of the §4.4 table, especially modifier-clicks and `data-no-pjax` inheritance |
| `navigate.ts` | Injected fake fetch: ok / not-ok / `{redirect}` / missing `_ViEW_` / abort / redirect loop hits the 5-hop cap |
| `navigate.ts` history | **Every row of the §4.2 table**, since that table is where the first design was wrong. Table-driven over (trigger × outcome), asserting which of push/replace/neither ran and with what URL. `popstate` + redirect gets its own case: it must `replaceState` the target, never leave the URL behind |
| invariants | After any settled `visit`: the address bar equals the mounted page's URL, and no two consecutive history entries are identical |
| `scroll.ts` | Offset saved on leave, restored on popstate |
| `progress.ts` | Hidden before 100ms, shown after, removed on completion and on error |

Server side: a Go test that one handler returns `{redirect}` under `X-Pjax: true` and a 302 without
it, plus `Vary: X-Pjax` on both.

Manual checklist (things unit tests will not catch): login failure keeps the typed username; login
success lands on the dashboard with the URL at `/admin`; back returns to `/admin/login` and the
server bounces to the dashboard; Cmd-click opens a new tab; rapid double-clicks render the second
target; disabling JavaScript leaves every link and form working.

## 8. Rollout order

1. inertia: `Redirect` + `Vary`, with tests → **maintainer tags and pushes**
2. Template: bump `go.mod`, switch admin to `c.Redirect`, add the `LoginForm` check
3. Client: new `pjax/` modules with tests; delete `pjax-loader.ts`, `Link.vue`, the `InertiaLink`
   export; `boot()` seeds history and enables delegation
4. Verify: vitest, `pnpm build`, the manual checklist, and `go test ./...`

Steps 2 and 3 are independently shippable once step 1 lands; the client tolerates a server that
still 302s (fetch follows the redirect), it just cannot fix bug 4 until the server changes.

## 9. Open risk

`pnpm type-check` is broken in the template as shipped — `vue-tsc@3.3.8` calls
`require.resolve('typescript/lib/tsc')`, which `typescript@7.0.2` no longer exports. This is why
`import.meta.glob` went untyped for so long. It is being fixed separately, but until it is, type
errors in the new modules will not be caught by CI.
