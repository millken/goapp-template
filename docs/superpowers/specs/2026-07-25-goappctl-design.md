# goappctl — v1 Design Spec

Date: 2026-07-25 · Status: Draft (rev 2, post-review) · Scope: deliberately small

> **rev 2 changes** (from reviewing the spec against the actual tree): `config.yaml` is
> gitignored and untracked, so a tracked `config.example.yaml` is introduced (§4 step 5, §7.1);
> `internal/app/services.go` and `server/server.go` are added as marker sites and the §7.3
> linchpin rule is narrowed to say why `svc.DB` and `svc.Session` differ; blank imports need
> markers because goimports cannot drop them (§5); the app *name* is separated from the module
> path (§4 step 6); `go.work` / `docs/` / tooling command registration are added to the delete
> list; verification is `build + vet + test` over 4 combos (§9); package.json dependency editing
> is dropped entirely (§4 step 4); `README.md` becomes a marker site with a Markdown comment form,
> after being fixed against the current tree (§5, §7.6).

## 1. Goal & Summary

`goappctl` is a small CLI that turns a fresh clone of `goapp-template` into a project:
`goappctl init` trims the current directory down to the selected components (db, session, admin,
ssr) and rewrites the project identity; `goappctl gen` scaffolds new resources into an existing
project (absorbing the current `internal/scaffold`). It is an **in-place transform** on the cwd —
no embedded template, no registry, no config files. v1 hardcodes four components and a
dead-simple comment-marker convention; everything fancier is a non-goal.

## 2. Non-goals (v1)

Explicitly out of scope — do not build:

- Component manifest / registry (hardcode the four components; see §6.3 extensibility note)
- `when=` expression DSL, `&&` / `!` conditions on markers
- `uncomment` / alternate-code blocks (nil-tolerant APIs make them unnecessary, §4 step 4)
- `lint-template` command, presets file, `.goappctl.yaml` project marker
- `goappctl create <name>` (fresh-repo generator via embed — possible later thin wrapper over init)
- `goappctl upgrade` / drift-protection tooling
- Full 16-combo compile matrix, boot smoke tests, frontend build in CI (§9 covers what we do run)

## 3. Model: in-place transform, tool in-repo

- goappctl lives **in this repo** at `cmd/goappctl` (not a separate repo).
- It operates on the **current directory**, which is expected to be a clone of the template.
- **Primary invocation is `go run ./cmd/goappctl`** — the tool and the template it transforms are
  then guaranteed to be the same revision, which removes version skew by construction:

```
git clone <goapp-template> myapp && cd myapp
go run ./cmd/goappctl init --module github.com/me/myapp --with db,session,admin,ssr
```

- `go install github.com/millken/goapp-template/cmd/goappctl@latest` also works and is documented
  as the secondary path, with the caveat in §11.
- Because the template *is* the cwd, there is **no embed, vendoring, sync, or drift**: the tool
  version and template version travel together in one repo. This is the whole reason the model
  stays trivial.
- Project detection (for `gen`): `go.mod` present + expected directories (`internal/controller/`,
  etc.). No marker file.

### 3.1 Guardrails (init is destructive and in-place)

`init` deletes and rewrites files in the cwd with no backup, so it refuses to run unless:

1. `go.mod` exists and its module path is exactly `github.com/millken/goapp-template`. This is
   also the idempotency guard — after a successful init the module path has changed, so a second
   `init` fails loudly instead of half-transforming an already-transformed tree.
2. The git worktree is clean, or `--force` is passed (allows non-repo directories too).

`--dry-run` prints the full plan (files to delete, marker blocks to strip, identity replacements)
and exits without touching anything.

## 4. `goappctl init`

```
goappctl init --module <path> [--name <app>] [--with db,session,admin,ssr]
              [--dry-run] [--force] [--git-reinit]
```

If `--with` is omitted, show an interactive checklist built from the hardcoded component list.
`--name` defaults to the last segment of `--module` (see step 6).

Pipeline (all steps operate on cwd, in order):

1. **Check guardrails.** §3.1. On `--dry-run`, compute every subsequent step's plan and print it
   instead of applying.
2. **Parse selection.** Validate `--with` names against the hardcoded list; apply dependency
   auto-closure: `admin ⇒ session + db` (inform the user when closure adds components).
3. **Delete owned files.** For each *unselected* component, remove its owned dirs/files (§6.1).
   Missing paths are tolerated (no error); every deletion is logged.
4. **Strip marker blocks.** In the marker sites listed in §6.2, delete every
   `//goappctl:<name>` … `//goappctl:end` block whose name is unselected; for selected names,
   remove only the marker lines. Blocks named `tooling` are *always* deleted (§5.3).
   `frontend/package.json` is edited as JSON since JSON has no comments — **scripts only, never
   dependencies** (§4a).
5. **Materialize config.** Copy the marker-stripped `config.example.yaml` to `config.yaml` (skip
   if `config.yaml` already exists). `config.example.yaml` stays in the project as the tracked
   sample; `config.yaml` remains gitignored. Without this step a generated project has no config
   file at all, because the template's `config.yaml` is untracked.
6. **Rewrite identity.** Two independent dimensions:
   - *Module path*: global replace `github.com/millken/goapp-template` → `--module` across
     `*.go`, `go.mod`, `Makefile`, `*.md`, `.vscode/*.json`.
   - *App name*: replace the app name `myapp` / env prefix `MYAPP` → `--name` / `upper(--name)` in
     `internal/buildinfo/buildinfo.go` (`AppName` — the single source of truth; `commands/paths.go`
     derives `MYAPP_HOME` and `~/.myapp` from it at runtime, so no logic there needs rewriting,
     only its doc comments), `Makefile` (`BINARY`), `.vscode/launch.json` (`MYAPP_HOME`, two
     occurrences), and `frontend/package.json` (`name`).
     **Not** `commands/paths_test.go`: its `{"myapp", "MYAPP_HOME"}` entries are a table-driven test
     of the name→env-var function that sets `buildinfo.AppName` itself, so the literals are
     intentional and must stay.
   These are separate because the binary name is not necessarily the module's last segment; the
   default just makes it so.
7. **Remove template-only files.** Delete:
   - Tooling: `cmd/goappctl/`, `internal/scaffold/`, `commands/gen.go` (the `newGenCmd()`
     registration in `commands/root.go` is removed by the `tooling` marker in step 4).
   - Workspace: `go.work`, `go.work.sum` — they point at local sibling checkouts
     (`../../dnsoa/go/sqldb`, `../inertia`) and would break any build outside the author's machine.
   - Template docs: `docs/` (the template's own design specs are not the user's).
   - Template CI: `.github/workflows/goappctl.yml` (§9). `ci.yml` is kept — it is a reasonable
     starting CI for the generated project.
   - Local dev leftovers if present: `app.db`, `bin/`.
   Generated projects ship no generator.
8. **Clean up.** Run `goimports` (as a library, §11) then `go mod tidy`. This is what makes
   markers cheap: *ordinary* dangling imports and unused go.mod deps are removed automatically —
   see §5.2 for the blank-import exception, which markers must cover explicitly.
9. **Verify.** `go build ./...`, `go vet ./...`, `go test ./...` — all three; fail loudly if any
   breaks. `go build` alone does not compile `_test.go` files or build-tagged files, so it would
   ship a project whose `make test` is broken (§7.5) and whose prod path is unchecked (§11).
   Then optionally `rm -rf .git && git init` (`--git-reinit`).

### 4a. `frontend/package.json`: scripts only

Removing SSR does **not** remove any dependency: the only SSR-specific things in package.json are
the `build:ssr` script and the `run-s` fan-out in `build`. Every dependency (`vue`, `vite`,
`@vitejs/plugin-vue`, `npm-run-all2`, …) is needed by the client build regardless.

So init's only package.json edit when ssr is off is:

```
"build": "run-s build:client build:ssr"   →   "build": "vite build"
"build:ssr": …                            →   removed
"build:client": …                         →   removed (folded into build)
```

**Dependencies are never touched.** This deliberately avoids invalidating `pnpm-lock.yaml`
(`pnpm install --frozen-lockfile` would fail against a lockfile that still lists a removed dep),
and it shrinks the JSON-formatting-churn risk to a two-key edit. An unused `npm-run-all2` in a
non-SSR project is a harmless wart, and the right kind of wart to accept in v1.

## 5. Marker convention

Single-name whole-line comment markers. No expressions, no nesting semantics beyond "delete or
unwrap the block".

```
//goappctl:<component>
... lines owned by <component> ...
//goappctl:end
```

Three comment forms, one per file type:

| File type | Form |
|---|---|
| Go, TS | `//goappctl:<name>` … `//goappctl:end` |
| YAML | `#goappctl:<name>` … `#goappctl:end` |
| Markdown | `<!--goappctl:<name>-->` … `<!--goappctl:end-->` |

`frontend/package.json` is not markered (§4a). The existing `// gen:mounts:begin/end` region in
`mount_gen.go` keeps its current name (used by `gen`, not `init`).

Markdown markers are HTML comments, so they are invisible in rendered output — marker density in
`README.md` costs the reader nothing (§7.6).

Go (`commands/serve.go`):

```go
//goappctl:session
sess := session.New(cfg.Session, svc.DB)
svc.Session = sess
//goappctl:end
```

YAML (`config.example.yaml`):

```yaml
#goappctl:db
db:
  driver: sqlite3
  dsn: data/app.db
#goappctl:end
```

### 5.1 Strictness rules

The parser is strict, because silent misbehaviour here only surfaces as a compile error much later
(step 9) or, worse, as wrong code that compiles:

- An unclosed marker block is a **hard error** naming the file and line — never "strip to EOF".
- Nesting is **not allowed**; an opening marker inside an open block is a hard error.
- An **unknown component name** is a hard error (not a silent no-op). It means the tool and the
  template disagree, and failing at step 4 with a clear message beats failing at step 9 with a
  compile error.
- A `//goappctl:end` with no open block is a hard error.

### 5.2 Blank imports must be inside marker blocks

`goimports` removes *unused* imports, but a blank import (`_ "…"`) is never "unused" — it is
removed by nobody. `commands/serve.go` has exactly this case:

```go
//goappctl:db
_ "github.com/millken/goapp-template/internal/driver"
//goappctl:end
```

Without the marker, stripping db deletes `internal/driver/` while `serve.go` still imports it, and
the build fails. So the claim "no import-block markers exist" holds only for ordinary imports:
**every blank import of a component-owned package must be wrapped in a marker block.** Current
occurrences: `commands/serve.go:19`, and any `_ "…/internal/driver"` in test files (§7.5).

### 5.3 Reserved name: `tooling`

`tooling` is a reserved pseudo-component that is **always** stripped — it marks the template's own
generator and template-maintenance code, which no generated project keeps:

```go
//goappctl:tooling
root.AddCommand(newGenCmd())
//goappctl:end
```

It is not selectable via `--with` and does not appear in the interactive checklist. Current uses:
the `gen` command registration in `commands/root.go`, and
`TestExampleConfig_MarkersAreWellFormed` in `internal/config/example_test.go` — a test that asserts
every component still has a marker block, i.e. exactly what `init` removes.

### 5.4 Blank-line hygiene

Stripping a block leaves the blank lines that surrounded it, and `gofmt` does not collapse them
(it tolerates a blank line before a closing brace). Verified on
`internal/config/example_test.go`. So the stripper must, after removing a block, collapse a run of
blank lines at the seam down to at most one — otherwise every stripped component leaves a visible
scar in the generated project.

## 6. Components (hardcoded)

### 6.1 Owned files/dirs (deleted when the component is off)

| Component | Deps | Owned paths |
|---|---|---|
| `db` | — | `internal/service/db/`, `internal/driver/` |
| `session` | — | `internal/service/session/` |
| `admin` | session, db | `internal/controller/admin/`, `commands/admin_user.go`, `frontend/pages/admin/`, `frontend/src/components/AdminLayout.vue` |
| `ssr` | — | `frontend/ssr/`, `frontend/ssr-esm-render.ts`, `frontend/vite.config.ssr.ts` |

### 6.2 Marker sites (shared files that survive with blocks stripped)

This is the authoritative inventory — the prerequisite PRs in §7 add exactly these markers.

| File | Marked names | What the blocks cover |
|---|---|---|
| `commands/serve.go` | db, session, admin | infra construct/Start/Stop, `svc.*` assignment, `eng.Use(session middleware)`, `admin.New/Validate/Mount`; **blank driver import** (§5.2) |
| `commands/root.go` | admin, `tooling` | `newAdminCmd()` registration; `newGenCmd()` registration |
| `internal/app/services.go` | session | the `Session *session.Service` field + its import (§7.3 — *not* `DB`) |
| `internal/config/config.go` | db, session, admin, ssr | `Config.DB/.Session/.Admin` fields + imports; `ServerConfig.SSR` / `.SSRBundlePath`; the SSR entries in `defaults()` |
| `server/server.go` | ssr | `inertia/ssr` + `ssr/quickjs` imports, `ssrBundleName` const, `cfg.SSR`→`ModeSSR` branch, the `quickjs.NewVM` block, the `ModeSSR` arm of `modeName` |
| `server/mode_dev.go` | ssr | `loadSSRBundle` (build tag `!prod`) |
| `server/mode_prod.go` | ssr | `loadSSRBundle` (build tag `prod`) |
| `config.example.yaml` | db, session, admin, ssr | the config sections |
| `README.md` | db, session, admin, ssr, `tooling` | see §7.6 |
| `frontend/package.json` | — | JSON edit, scripts only (§4a) |
| `internal/config/example_test.go` | db, session, admin, ssr, `tooling` | per-section assertions; the template-only marker-hygiene test |
| other test files (§7.5) | db, session | cross-component fixtures and blank driver imports |

Deliberately *not* a marker site: `frontend/tsconfig.node.json`, whose `include` array lists
`ssr-esm-render.ts` / `ssr/**/*` (plus two files that don't even exist today). TypeScript ignores
`include` entries that match nothing, so stale SSR entries are harmless — not worth a JSON edit.

`ssr` is the messiest component by a wide margin — four Go files, two of them behind opposing
build tags. Budget accordingly; it is the one most likely to need a second pass.

### 6.3 Notes

- **Samples:** there is currently nothing to strip. `internal/controller/site/site.go` contains
  only Home + Health (both always kept) and `frontend/pages/` contains only `Home.vue` plus the
  admin pages (owned by `admin`). A `--no-samples` flag would be a no-op, so **v1 has no
  `--no-samples` flag**. Reintroduce it if and when the template grows real sample resources.
- **Extensibility plan:** when a 5th component lands (grpc/restful/buf), extract a small in-code
  registry (`[]Component{Name, Deps, OwnedPaths, MarkerSites}`) from the hardcoded switch —
  *then*, not now.

## 7. Prerequisite template changes (land before goappctl v1)

Small PRs against the template itself, in this order.

### 7.1 Add `config.example.yaml` (blocking — do this first) — ✅ landed

`config.yaml` is listed in `.gitignore` and is **not tracked**, so a fresh clone does not contain
it. Add a tracked `config.example.yaml` carrying the full annotated config with `#goappctl:<name>`
markers; keep `config.yaml` gitignored. `config.Load` already tolerates a missing file, so this is
additive and breaks nothing. This unblocks §4 step 5 and the `config.example.yaml` row of §6.2.

Shipped with two guards in `internal/config/example_test.go`, so the example cannot rot silently:

- `TestExampleConfig_Parses` — every section must still unmarshal into `Config` (a renamed yaml tag
  would otherwise turn a documented key into a no-op). Per-component assertions are inside marker
  blocks so the test survives stripping.
- `TestExampleConfig_MarkersAreWellFormed` — enforces §5.1 (no unclosed, nested, unknown or
  duplicate blocks) and requires all four components to be present. Wrapped in `tooling` (§5.3).

### 7.2 Add the markers

Add `//goappctl:<name>` … `//goappctl:end` blocks per the §6.2 inventory, and make composition
**line-oriented**: one component per line/block in `serve.go` (no `db+session+admin` crammed onto
one line), so whole-line stripping works. Wrap blank imports per §5.2.

### 7.3 The linchpin: cross-component references go through `app.Services`

**Cross-component references in shared files go ONLY through `app.Services` fields, never through
another component's local variable or package.** This is what lets single-name markers work with
zero alternate blocks. Concretely, `serve.go` becomes:

```go
svc := app.NewServices(log)          // unconditional; no component args
//goappctl:db
dbSvc := db.New(cfg.DB); …Start…
svc.DB = dbSvc.DB()
//goappctl:end
//goappctl:session
svc.Session = session.New(cfg.Session, svc.DB)   // svc.DB may be nil → memory store
//goappctl:end
```

Required API changes: `NewServices(log)` drops its `db`/`sess` parameters (fields are assigned
after construction), and `session.New` takes `*sqldb.DB` directly (nil-tolerant) instead of the
`db.Provider` interface, so the session package does **not** import the db component.

**`DB` and `Session` are not symmetric — this is the one subtlety in the whole design:**

- `Services.DB *sqldb.DB` **stays a core field, always**. `sqldb` is a third-party library, so the
  field's type survives even when the db component is deleted; `svc.DB` is simply `nil`. The cost
  is that `sqldb` remains a core go.mod dependency regardless — the one accepted wart.
- `Services.Session *session.Service` **cannot** be core, because its type comes from
  `internal/service/session/`, a package that init *deletes*. So `services.go` is itself a marker
  site (§6.2), and it follows that:

  > **Rule: core (non-component) code must never reference `svc.Session`.** Only the `session` and
  > `admin` blocks/packages may. Today only `internal/controller/admin/` does (`auth.go:18`,
  > `handlers.go:39`, `handlers.go:49`), and admin's dependency closure guarantees session is
  > present. Adding a `svc.Session` reference to `internal/controller/site/` or any other core file
  > silently breaks the minimal combo — combo 2 in §9 is the guard.

`admin` legitimately requires db+session, so its blocks may freely use `svc.DB` / `svc.Session`.

### 7.4 Keep `gen:mounts:begin/end` unchanged

The generator region name stays as-is; `init` never touches it.

### 7.5 Make tests survive component removal

`go test ./...` is part of verification (§4 step 9), so cross-component test files must be handled
rather than ignored. Markers work in `_test.go` files at no extra cost, so prefer markers over
splitting files. Known cases:

- `internal/app/services_test.go` — imports both session and db; needs db/session marker blocks.
- `internal/service/session/store_test.go` — imports `internal/driver` (blank) to exercise the DB
  store; wrap the DB-store cases and that import in a `db` block (§5.2).
- `internal/controller/admin/*_test.go` — no action: they live in an admin-owned directory and
  admin's closure guarantees db+session.

### 7.6 Marker `README.md` (and fix it first)

Identity rewriting (§4 step 6) only swaps names inside `README.md` — it deletes nothing. Without
markers, a generated project ships documentation for features it does not have: a whole section on
a generator that was deleted, an SSR workflow for a stripped component, and a `go.work` guide for a
deleted file.

**Prerequisite before markering: the README is already stale.** It still documents the pre-`22cd24d`
architecture — `app.Module`, `internal/module/{db,session,admin}/` (now `internal/service/`),
`server/routes.go`, `frontend/ssr-build.ts`, `ssr-modules.ts`, `frontend/scripts/`, `pnpm generate`
— none of which exist. Markering a stale README just makes init strip lies faithfully. Fix the
README against the current tree *first*, in its own PR, then add markers.

Marker plan (section granularity except where noted):

| README location | Name |
|---|---|
| `- **SSR**：QuickJS…` bullet in the intro | ssr |
| 项目结构 tree — per-component line groups (`internal/driver/`, `internal/service/db/`, `…/session/`, `…/controller/admin/`, `frontend/ssr*`) | db / session / admin / ssr |
| 项目结构 tree — `commands/gen.go`, `internal/scaffold/` lines | `tooling` |
| CLI block — `gen resource` / `gen admin` lines | `tooling` |
| CLI block — `admin create-user` line | admin |
| 「登录需要一个 admin 用户」note | admin |
| 「脚手架生成器」section (whole) | `tooling` |
| 「SSR 工作流」section (whole) | ssr |
| 「Build tags」section — the SSR bundle sentence only | ssr |
| 「自定义为新项目」section (whole) | `tooling` |
| 「本地依赖（开发者）」section (whole) | `tooling` |

Two simplifications that remove marker clusters rather than adding them:

- **The 配置 section should not inline the YAML.** It currently duplicates the full config, which
  would need db/session/admin/ssr markers a second time and would drift from
  `config.example.yaml`. Replace the inline YAML with a pointer to `config.example.yaml` (§7.1);
  one marked copy of the config, not two.
- **「自定义为新项目」is obsoleted by goappctl itself** — its four manual steps are exactly what
  `init` automates. In the template, rewrite it to point at `go run ./cmd/goappctl init`; in a
  generated project it is meaningless, hence `tooling`.

## 8. `goappctl gen`

```
goappctl gen resource <Name> [--admin] [--no-mount]
```

- Reuses the existing `internal/scaffold` code, moved into `cmd/goappctl` (templates keep
  `[[ ]]` delimiters).
- Detects module path from `go.mod`; detects project shape by directory presence (no marker file).
- Emits: controller embedding `*app.Services` + `Mount(...)`, model, Vue pages into
  `internal/controller/<pkg>/` and `frontend/pages/<pkg>/`.
- Auto-edits the `gen:mounts` region in `internal/controller/mount_gen.go`; idempotent (re-running
  doesn't duplicate — the router's `ErrDuplicateRoute` / `Engine.RegistrationError()` is the
  runtime backstop). `--no-mount` skips the edit.
- `--admin` targets the admin area; errors clearly if `internal/controller/admin/` is absent.
- **db-less projects:** the scaffolder writes migrations to `internal/service/db/migrations/`,
  which does not exist when db was stripped. If that directory is absent, skip migration emission
  with a visible warning (the generated handler compiles fine — its `ct.DB` usages are commented
  TODOs), and note in the warning that `svc.DB` is nil at runtime.

## 9. Correctness (v1)

A dedicated workflow `.github/workflows/goappctl.yml` (deleted from generated projects, §4 step 7)
copies the repo into a temp dir — **excluding `go.work*`, which would otherwise point the copy at
non-existent sibling checkouts** — runs `init`, and requires `go build ./...`, `go vet ./...` and
`go test ./...` to pass, for **four** combos:

1. **All-on** — `--with db,session,admin,ssr`
2. **Minimal** — core only (no components). The combo most likely to expose a missed marker, and
   the guard for the §7.3 `svc.Session` rule.
3. **Middle** — `--with db,session,admin` (no ssr)
4. **SSR-only** — `--with ssr`. Deliberately included because ssr's marker sites are the most
   scattered (four Go files, two build tags) and this is the only combo that strips db+session
   while keeping ssr.

That's it — no 16-combo matrix, no boot smoke, no frontend build. Just enough to catch broken
marker stripping and deletion lists.

## 10. Internal package layout

```
cmd/goappctl/
  main.go            # cobra root: init, gen, version
  internal/
    initcmd/         # pipeline steps 1–9 (guardrails, selection, delete, strip, config,
                     # identity, remove, tidy, verify)
    markers/         # find/strip goappctl blocks (Go//TS `//`, YAML `#`, MD `<!-- -->`) + JSON edit
    components/      # the hardcoded four: names, deps, owned paths, marker sites
    scaffold/        # moved from internal/scaffold (gen)
```

Keep it flat; no interfaces until the 5th component forces the registry extraction.

## 11. Open questions / risks

- **Marker rot:** nothing enforces markers stay correct as the template evolves; the 4-combo CI is
  the only guard. Acceptable for v1 (a `lint-template` check is an explicit non-goal). The §7.3
  `svc.Session` rule is the specific thing most likely to rot.
- **The prod/SSR path is never verified.** `server/mode_prod.go` is behind `//go:build prod`, so
  `go build ./...` skips it, and `go build -tags prod ./...` needs `server/embedded/dist` — a
  gitignored directory that only exists after a frontend build. Since §2 rules out frontend builds
  in CI, **SSR breakage in the prod build path can ship**. Mitigation for v1: keep the ssr markers
  in `mode_prod.go` byte-identical in shape to `mode_dev.go` so they rot together, and accept the
  gap explicitly. Revisit if it bites.
- **`goimports` availability:** shelling out to a `goimports` binary would fail on most user
  machines. Import `golang.org/x/tools/imports` as a library instead. This costs nothing in the
  generated project: `cmd/goappctl` is in the *same module*, so step 7 deletes it and step 8's
  `go mod tidy` drops `x/tools` from the generated `go.mod` automatically. The step order
  (remove-then-tidy) is load-bearing for this.
- **`go mod tidy` needs network** on first run if the module cache is cold. Acceptable; surface the
  error clearly rather than papering over it.
- **package.json JSON edit:** even the scripts-only edit must preserve formatting well enough not
  to churn diffs — use a key-level edit, not a full re-marshal.
- **Interactive checklist dependency UX:** when the user picks admin, auto-check db/session in the
  UI vs. closure-after-confirm — implementer's choice, just be visible about it.
- **cgo after stripping:** db-off must drop `mattn/go-sqlite3` and ssr-off must drop
  `buke/quickjs-go` (both cgo) via tidy, leaving a pure-Go build. Combo 2 in §9 is the check —
  assert on the tidied `go.mod`, not just on a successful build.
- **`go install @latest` skew:** a user may run a newer goappctl against an older clone. v1 answer:
  document `go run ./cmd/goappctl` as the primary path (§3), which makes skew structurally
  impossible; for the `@latest` path, §5.1's hard error on unknown component names turns skew into
  an immediate, legible failure instead of a mysterious build break.
