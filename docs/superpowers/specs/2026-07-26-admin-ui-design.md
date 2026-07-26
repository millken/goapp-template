# Admin UI Components — Design Spec

Date: 2026-07-26 · Status: Validated — approved for planning · Depends on: the
`admin` component (session + db)

## 1. Goal

Give the admin area a real component vocabulary — table, dialog, select,
dropdown menu, pagination — so `goappctl gen admin <name>` produces a usable
back office instead of a `<ul>`, and so the shell and login page stop being
hand-rolled Tailwind one-offs.

The constraint that shapes everything: this is a *template*. Whatever lands must
disappear cleanly when `goappctl init` runs without `admin`, and must not make
the public side of a derived project heavier.

## 2. Decisions

| Question | Decision |
|---|---|
| Library | **shadcn-vue** (`styles/default`) + **`@tanstack/vue-table`** for data tables |
| Distribution | **Source copied into the repo**, not an npm component library |
| Scope | **Admin only.** `gen resource` templates stay plain Tailwind |
| Ownership | The copied files belong to the **`admin` component** — no new component |
| Adding components later | **`goappctl gen ui <name>`**, not the upstream CLI (§6) |
| Client-side form validation | **Excluded.** No shadcn `form`, no vee-validate — validation is server-side (`internal/validate`) |
| Vue feature flags | **Delete the five `__VUE_FEATURE_*` defines** — they are dead config (§5) |
| Theme CSS | Marker block in `main.css`; requires `.css` support in `markers` |
| npm dependencies | **Left in place** when `admin` is off — pruning them invalidates `pnpm-lock.yaml` (§5.3) |

**Why shadcn-vue over five alternatives.** Measured, not assumed — every
candidate was built into this repo, rendered through the real QuickJS SSR VM, and
weighed by actual gzip bytes of `frontend/dist/assets`. Numbers in §10.
shadcn-vue wins on size at equal feature coverage (102.5 kB vs naive-ui's 161.0
for the same eight components plus a sortable, paginated table), and it is the
only candidate whose styles come from the static Tailwind sheet — so it has no
FOUC, while naive-ui and PrimeVue would each need style collection added to
`frontend/ssr/render.ts`. arco-design is **disqualified**, not merely heavy: four
of its components break the SSR bundle at top-level init (§10).

**Why copied source, not a dependency.** It matches how this template already
works: `gen resource` output is "ordinary files, yours to edit; the generator
doesn't lock them down." The copied components are the same deal. Three
consequences that matter here: the bundle contains only what was copied;
trimming is a path deletion, needing no new mechanism; and there is no upstream
version that can break a derived project. The cost is that upstream fixes do not
arrive automatically.

**Why admin-only.** `db` and `session` each have a consumer; a UI layer usable
from public pages would have none in the template while making every public page
pay. Measured cost of enabling it for public pages: +21.5 kB gzip (§10). Keeping
it inside `admin` also preserves a property `gen resource` has today — it
compiles and works in a build with neither `db` nor `session`.

**Why no client-side form validation.** The validator spec settled this: errors
never enter the session, and a failed submit re-renders the same request with an
`errors` prop. A vee-validate-based `form` component would be a second, parallel
mechanism for a job already done, and would pull a dependency in to do it.

## 3. Non-goals

- **A `ui` component of its own.** The files ride with `admin`.
- **Public-page components.** `templates/resource/*` are not touched.
- **Automatic dependency installation.** `gen ui` prints the `pnpm add` line; the
  developer runs it, exactly as `gen admin` prints its mount line.
- **Mirroring the upstream CLI's feature set** (themes, registries, `diff`).
  `gen ui` fetches and writes; nothing more.
- **A design system.** Colours and radii stay at shadcn's `neutral` defaults;
  restyling is the derived project's business.
- **Dark mode wiring.** The theme block defines `.dark` variables because they
  come with the theme, but nothing toggles the class. A later concern.

## 4. Ownership and trimming

`cmd/goappctl/internal/components/components.go` — the `admin` entry's `Owned`
grows by two paths:

```go
{
    Name: "admin",
    Deps: []string{"session", "db"},
    Owned: []string{
        "internal/controller/admin",
        "commands/admin_user.go",
        "frontend/pages/admin",
        "frontend/src/components/AdminLayout.vue",
        "frontend/src/components/ui",   // copied shadcn components
        "frontend/src/lib",             // cn() / valueUpdater()
    },
},
```

`frontend/src/lib/utils.ts` holds `cn()` (clsx + tailwind-merge) and
`valueUpdater()` (a TanStack Table helper). Both exist only for the copied
components, so the directory is admin-owned rather than shared.

**Existing files are rewritten in place, not duplicated:**

- `frontend/src/components/AdminLayout.vue` — the shell, rebuilt on
  `Button`/`Alert`/`Separator`. Same props, same contract, same path. A second
  parallel layout would only invite drift.
- `frontend/pages/admin/login.vue` — `Card` + `Label` + `Input` + `Button`.
- `frontend/pages/admin/dashboard.vue` — stays a dashboard, restyled with `Card`.
- `templates/admin/index.vue.tmpl` — the `<ul>` becomes a `Table` with the
  TanStack row model, plus `DropdownMenu` row actions and `Pagination`.
- `templates/admin/form.vue.tmpl` — `Label` + `Input` + `Button`, keeping the
  `errors?.<field>` rendering the validator spec added.

**Navigation needs no adapter.** `Button as="a" :href="…"` renders a plain
anchor, which the existing PJAX layer already intercepts through document-level
delegation. No router integration, no `Link` component, no glue.

## 5. Build configuration

### 5.1 Delete the dead Vue feature flags

Both vite configs define five flags that **nothing reads**. Verified: Vue
3.5.40's `runtime-core.esm-bundler.js` references only `__VUE_OPTIONS_API__`,
`__VUE_PROD_DEVTOOLS__` and `__VUE_PROD_HYDRATION_MISMATCH_DETAILS__`, and no
package under `node_modules` mentions `__VUE_FEATURE_` at all. Flipping
`__VUE_FEATURE_TELEPORT__`/`TRANSITION__` from `'false'` to `'true'` produced a
byte-identical SSR bundle, byte-identical SSR HTML (17489 bytes both), and an
identical client bundle (110.3 kB both).

So remove these five lines from `vite.config.ts` **and** `vite.config.ssr.ts`:

```
__VUE_FEATURE_SUSPENSE__  __VUE_FEATURE_TELEPORT__  __VUE_FEATURE_TRANSITION__
__VUE_FEATURE_KEEP_ALIVE__  __VUE_FEATURE_SCOPED_SLOT__
```

They are worse than inert: they read as though Teleport were disabled on
purpose, which is a live trap for anyone reasoning about whether an overlay
component can render.

**The invariant that does matter is preserved.** Commit `253acad` introduced
these defines to keep client and SSR compiling Vue identically and avoid
hydration divergence. That reasoning is sound and applies to the three real
flags, which are already `'false'` in both configs and stay that way. Any future
change to one config must be mirrored in the other.

### 5.2 Theme CSS, trimmable

`frontend/src/styles/main.css` gains the shadcn theme — `@import
"tw-animate-css"`, a `@custom-variant dark`, `:root`/`.dark` custom properties,
and an `@theme inline` block mapping each colour to Tailwind's `--color-*`
namespace (that mapping is what makes `bg-primary`, `border-input`, `ring-ring`
resolve). All of it wrapped:

```css
@import "tailwindcss";
/*goappctl:admin*/
@import "tw-animate-css";
… theme variables and @theme inline …
/*goappctl:end*/
```

This requires one new entry in `cmd/goappctl/internal/markers/markers.go`:

```go
".css": {open: "/*goappctl:", end: "/*goappctl:end*/", close: "*/"},
```

Not optional: `initcmd` deliberately **errors** when a marker appears in a file
type with no comment form (`initcmd.go:264`), so markers in `.css` without this
entry would break `goappctl init` outright. That guard is correct and stays.

### 5.3 Dependencies stay — they are not pruned

Eight entries exist only for the copied components:

| | packages |
|---|---|
| `dependencies` | `reka-ui`, `@vueuse/core`, `lucide-vue-next`, `class-variance-authority`, `clsx`, `tailwind-merge`, `@tanstack/vue-table` |
| `devDependencies` | `tw-animate-css` |

An earlier revision of this spec had `init` remove them when `admin` was off, on
tidiness grounds. **That was wrong and has been reverted.** `pnpm-lock.yaml` is
not pruned alongside `package.json`, and `.github/workflows/ci.yml` ships with
every generated project running `pnpm install --frozen-lockfile` — which fails
the moment the two disagree. Every admin-less project would have been generated
with a red frontend build, and `init`'s own verification (`go build`/`vet`/`test`)
would not have caught it.

This invariant predates this spec: `2026-07-25-goappctl-design.md` §4a already
stated that dependencies are never touched, naming this exact failure, and calling
an unused package "the right kind of wart to accept". That judgement holds.

What a trimmed project carries is therefore a wasted download, not weight —
unused dependencies cost **zero** bundle bytes, measured: removing five abandoned
libraries left every chunk hash byte-identical. Editing `scripts` (what
`stripSSRScripts` does for `ssr`) stays safe, because the lockfile does not track
them.

## 6. `goappctl gen ui <component>…`

```bash
goappctl gen ui table                 # one
goappctl gen ui table dialog select   # several
goappctl gen ui table --force         # overwrite existing files
goappctl gen ui table --dry-run       # print the plan, write nothing
goappctl gen ui table -C ../other     # target another project root
```

Behaviour, in order:

1. **Refuse early** when `internal/controller/admin/` is absent — same check
   `gen admin` makes. Without an admin area there is nothing to add components to.
2. **Fetch** `https://shadcn-vue.com/r/styles/default/<name>.json`.
3. **Resolve `registryDependencies` transitively**, deduplicating: `table`
   pulls `utils`, `pagination` pulls `button`.
4. **Write files.** Each `files[]` entry has a `path` and its `content`. A path
   starting `ui/` lands under `frontend/src/components/`; anything else (i.e.
   `lib/utils.ts`) lands under `frontend/src/`. Existing files are refused
   without `--force`, matching `gen resource`.
5. **Rewrite registry-internal imports:** `@/registry/default/ui` →
   `@/components/ui`. The upstream CLI does this from `components.json` aliases;
   a fetcher that skips it produces code that will not build — `Pagination*.vue`
   imports `buttonVariants` that way.
6. **Print, do not run,** the aggregated `pnpm add …` line for the union of the
   fetched items' `dependencies`, minus what `frontend/package.json` already has.

Error handling must distinguish the two failures a developer will actually hit:
a **404** means the component name is wrong (say which name), and a **transport
error** means the registry is unreachable (say so). Both exit non-zero without
writing partial output — fetch every requested item before writing any file.
`--dry-run` still needs the network, because the plan it prints is derived from
the fetched manifests.

**Why not the upstream CLI.** Primarily because of who has to run it. A developer
working on a generated project already has the Go toolchain — `goappctl` is how
this template does every other kind of scaffolding, and `gen ui` keeps adding a
component in the same place as adding a resource. The upstream CLI additionally
requires Node and a working `pnpm dlx`, which a Go backend developer may not have
set up at all.

The registry format is four fields (`files[].path`, `files[].content`,
`dependencies`, `registryDependencies`) and the fetch-and-rewrite path is
prototyped end to end. The exposure is that an upstream format change breaks
`gen ui`; the mitigation is that it breaks loudly, at a single call site, and
only for developers adding new components.

**A secondary observation, not the main argument.** Both `pnpm dlx
shadcn-vue@latest init` and the package's MCP server fail here with
`Failed to fetch from registry`, reproducibly, while `curl` and a plain
`node -e "fetch(...)"` against the identical URL return 200 — so the fault is in
that npm package's fetch path, not the network. An earlier draft of this section
leaned on that as the reason for `gen ui`; it is too environment-specific to
carry the decision, and the argument above stands without it.

## 7. Baseline component set

Twelve components plus the utils module, chosen by what the rewritten pages and
templates actually use:

`alert` · `badge` · `button` · `card` · `dialog` · `dropdown-menu` · `input` ·
`label` · `pagination` · `select` · `separator` · `table` (+ `lib/utils`)

Roughly 78 files and ~1,600 lines of copied source — a one-time cost that does
not grow with use. **`form` is deliberately absent** (§2). Anything else —
date picker, combobox, toast — is `gen ui` away.

## 8. Constraints worth writing down

- **Do not server-render an open overlay.** `Dialog`, `DropdownMenu` and
  `Select` project their floating content through Teleport, and Vue's SSR
  renderer puts teleported output in `ctx.teleports`, which
  `frontend/ssr/render.ts` does not collect. Verified: forcing
  `DialogRoot :open="true"` yields zero occurrences of `role="dialog"` in the SSR
  HTML. Overlays default to closed, so this is a non-issue in practice — but a
  page that opens one during SSR will hydrate against markup the server never
  sent. Unrelated to the feature flags in §5.1.
- **`reka-ui` cannot be swapped out** while keeping these components. It is the
  primitive layer: 14 of 15 `dropdown-menu` files import it, 11 of 12 for
  `select`, 8 of 9 for `pagination`, 7 of 10 for `dialog`. Only `alert`, `badge`,
  `card`, `input` and `table` are pure markup.
- **Components from other reka-ui kits are interchangeable.** sigma-ui and
  shadcn-vue both copy source over the same primitives, so an individual
  component can be lifted from either after fixing its import paths.
- **Client and SSR vite configs must keep the three real Vue flags mirrored**
  (§5.1).

## 9. Testing

- **`markers`**: the existing table-driven test gains a `.css` case — a block
  stripped when its component is off, unwrapped when on.
- **`initcmd`**: with `admin` off, `frontend/package.json` no longer lists the
  eight packages and `main.css` no longer contains the theme block; with `admin`
  on, both are untouched. The existing marker-in-unsupported-file guard keeps
  passing.
- **`gen ui`**: registry parsing, transitive `registryDependencies`, the
  `ui/` → `components/ui/` mapping, and the `@/registry/default/ui` rewrite, all
  driven by **local fixture JSON**. `cmd/goappctl` tests are offline today and
  must stay that way, so the HTTP fetch sits behind a small seam the tests
  substitute. Also: `--force` refusal, and a 404 producing a name error.
- **Generated code compiles**: `TestAdmin_OutputCompiles` already renders the
  admin templates into the real module tree and runs `go build`, so template
  changes are covered without new machinery.
- **`pnpm type-check`**: the whole copied set must pass under `strict: true`.
  Already verified during evaluation — 78 files, zero errors.
- **SSR**: one check that a rewritten admin page renders through the QuickJS VM
  and contains its table rows, menu and flash. The evaluation harness did this
  by hand; the spec's implementation should leave it as a test rather than a
  one-off.

## 10. Measurements

Real gzip bytes of `frontend/dist/assets` (not vite's reported figures); SSR
driven through `quickjs.NewVM` + `RenderComponent`. Same eight-component set
(alert, form/input, select, table, pagination, modal, button) except where noted.

The two baseline figures below are the same build measured two ways: **27.9 kB**
is shared JS + CSS only, **28.1 kB** adds the 0.3 kB `Home` page chunk so it can
be compared with a real page load.

| Candidate | QuickJS SSR | Total gzip | Has table |
|---|---|---|---|
| baseline (plain Tailwind) | ok | 27.9 kB | — |
| reka-ui primitives only | ok | 77.3 | no |
| **shadcn-vue (8 components)** | ok | **90.3** | styled `<table>` |
| **shadcn-vue + TanStack Table** | ok | **102.5** | sortable, paginated |
| arco-design (on-demand CSS) | **broken** | 118.2 | yes |
| PrimeVue unstyled | ok | 154.5 | yes |
| naive-ui | ok | 161.0 | yes |
| PrimeVue styled | ok | 167.6 | yes |
| tdesign-vue-next | ok | 197.5 | yes |

**arco's failure mode**, for the record: `Table`, `Form`, `Select` and
`Pagination` throw during *bundle top-level initialisation*, so neither SSR
export is ever assigned and every page — including pages with no arco in them —
fails with a bare `TypeError: not a function`. A `RenderTemplate` probe fails
too, which is how the top-level cause was established. `Button`, `Alert`,
`Input` and `Modal` render fine. Root cause unidentified; `Intl` (arco never uses
it), a Modal side effect, and a blocked `Function` constructor were each ruled
out. QuickJS provides none of `document`, `window`, `navigator`, `Intl`,
`ResizeObserver`, `IntersectionObserver`, `MutationObserver`, `matchMedia`,
`requestAnimationFrame`, `getComputedStyle`, `structuredClone`; the other four
libraries guard for that, arco's four components do not.

**Cost in place**, from the built demo (shell + list page with sorting, two
filters, pagination, row-action menu, delete dialog):

| | Public page | Admin page |
|---|---|---|
| Before | 28.1 kB | — |
| After | 49.6 kB | 109.4 kB |
| **`admin` trimmed** | **28.1 kB** | — |

The last row is the one that justifies §5.2 and §5.3. With nothing importing
`reka-ui`, the Vue runtime chunk returns to exactly its original 24.8 kB — the
split and growth are entirely a consequence of use, not of the dependency being
present. Strip the theme block from `main.css` too and a trimmed project is
byte-for-byte where it started.

Code volume, for scale: the rewritten shell is 73 lines against the current 66 —
using these components does not inflate day-to-day code. The weight is the
one-time ~1,600 copied lines, which is read rarely and edited rarely.

## 11. Files touched

| File | Change |
|---|---|
| `frontend/src/components/ui/**` | New: 12 copied components (~78 files) |
| `frontend/src/lib/utils.ts` | New: `cn()`, `valueUpdater()` |
| `frontend/src/components/AdminLayout.vue` | Rewritten on shadcn components |
| `frontend/pages/admin/login.vue` | Rewritten |
| `frontend/pages/admin/dashboard.vue` | Restyled |
| `frontend/src/styles/main.css` | Theme block inside `goappctl:admin` markers |
| `frontend/vite.config.ts` | Remove five dead `__VUE_FEATURE_*` defines |
| `frontend/vite.config.ssr.ts` | Same |
| `frontend/package.json` | Add the eight packages |
| `cmd/goappctl/internal/markers/markers.go` | `.css` comment form |
| `cmd/goappctl/internal/markers/markers_test.go` | `.css` case |
| `cmd/goappctl/internal/components/components.go` | Two paths on `admin` |
| `cmd/goappctl/internal/initcmd/initcmd.go` | New `stripAdminDeps` |
| `cmd/goappctl/internal/initcmd/initcmd_test.go` | Pruning cases |
| `cmd/goappctl/internal/scaffold/ui.go` | New: `gen ui` |
| `cmd/goappctl/internal/scaffold/ui_test.go` | New, fixture-driven |
| `cmd/goappctl/gen.go` | Register the `ui` subcommand |
| `cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl` | Table + row actions + pagination |
| `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl` | Label/Input/Button, keeping `errors?` |
| `README.md` | Component set, `gen ui`, the admin-only boundary |

`cmd/goappctl/internal/scaffold/templates/resource/*` and `internal/controller/**`
are **not** touched.

## 12. Implementation order

Larger than one sitting, and it splits cleanly at a seam. Two stages, each
leaving the tree green:

1. **The component layer.** Copy the twelve components, rewrite the shell, login,
   dashboard and the two admin templates, add the theme block plus the `.css`
   marker form, extend `admin`'s `Owned`, add `stripAdminDeps`, delete the dead
   defines. After this the template ships a working shadcn admin and trims
   cleanly — the whole user-visible payoff.
2. **`gen ui`.** The generator subcommand and its fixture-driven tests. Nothing
   in stage 1 depends on it; until it lands, adding a component means running the
   upstream CLI or copying by hand.

Stage 2 is genuinely optional-for-now: if `pnpm dlx shadcn-vue add` turns out to
work on other machines, its value drops to convenience. Worth reassessing at the
seam rather than committing up front.
