# goappctl — v1 Design Spec

Date: 2026-07-25 · Status: Draft — pending user review · Scope: deliberately small

## 1. Goal & Summary

`goappctl` is a small CLI that turns a fresh clone of `goapp-template` into a project: `goappctl init` trims the current directory down to the selected components (db, session, admin, ssr) and rewrites the module path; `goappctl gen` scaffolds new resources into an existing project (absorbing the current `internal/scaffold`). It is an **in-place transform** on the cwd — no embedded template, no registry, no config files. v1 hardcodes four components and a dead-simple comment-marker convention; everything fancier is a non-goal.

## 2. Non-goals (v1)

Explicitly out of scope — do not build:

- Component manifest / registry (hardcode the four components; see §6 extensibility note)
- `when=` expression DSL, `&&` / `!` conditions on markers
- `uncomment` / alternate-code blocks (nil-tolerant APIs make them unnecessary, §4 step 4)
- `lint-template` command, presets file, `.goappctl.yaml` project marker
- `goappctl create <name>` (fresh-repo generator via embed — possible later thin wrapper over init)
- `goappctl upgrade` / drift-protection tooling
- Full compile matrix, boot smoke tests, frontend build in CI (§9 covers what we do run)

## 3. Model: in-place transform, tool in-repo

- goappctl lives **in this repo** at `cmd/goappctl` (not a separate repo). Installable via
  `go install github.com/millken/goapp-template/cmd/goappctl@latest`.
- It operates on the **current directory**, which is expected to be a clone of the template:

```
git clone <goapp-template> myapp && cd myapp
goappctl init --module github.com/me/myapp --with db,session,admin,ssr
# then optionally: rm -rf .git && git init
```

- Because the template *is* the cwd, there is **no embed, vendoring, sync, or drift**: the tool version and template version travel together in one repo. This is the whole reason the model stays trivial.
- Project detection (for `gen`): `go.mod` present + expected directories (`internal/controller/`, etc.). No marker file.

## 4. `goappctl init`

```
goappctl init --module <path> [--with db,session,admin,ssr] [--no-samples] [--git-reinit]
```

If `--with` is omitted, show an interactive checklist built from the hardcoded component list. `--no-samples` strips the sample pages/resource.

Pipeline (all steps operate on cwd, in order):

1. **Parse selection.** Validate `--with` names against the hardcoded list; apply dependency auto-closure: `admin ⇒ session + db` (inform the user when closure adds components).
2. **Delete owned files.** For each *unselected* component, remove its owned dirs/files (see table §6), e.g. `internal/service/db/`, `internal/controller/admin/`, `commands/admin_user.go`, `frontend/pages/admin/`, SSR build files.
3. **Strip marker blocks.** In the ~5 shared files, delete every `//goappctl:<name>` … `//goappctl:end` block whose name is unselected; for selected names, just remove the marker lines. `frontend/package.json` is edited as JSON (remove keyed deps/scripts) since JSON has no comments.
4. **(Nothing.)** No alternate blocks ever get inserted — component constructors are nil-tolerant (e.g. `session.New(cfg, nil)` = memory store), which is *why* step 3 needs no conditionals. This is a template property (§7), not an init step, but the pipeline relies on it.
5. **Rewrite identity.** Global replace `github.com/millken/goapp-template` → `--module`; put project name (last module segment) into `package.json`, `README.md`, `config.yaml` header.
6. **Remove tooling.** Delete `cmd/goappctl/`, `internal/scaffold/`, `commands/gen.go`. Generated projects ship no generator.
7. **Clean up.** Run `goimports`, `gofmt`, `go mod tidy`. This is what makes markers cheap: dangling imports and unused go.mod deps are removed automatically, so **no import-block or go.mod markers exist**.
8. **Verify.** `go build ./...`; fail loudly if broken. Then optionally `rm -rf .git && git init` (`--git-reinit`).

## 5. Marker convention

Single-name whole-line comment markers. No expressions, no nesting semantics beyond "delete or unwrap the block".

```
//goappctl:<component>
... lines owned by <component> ...
//goappctl:end
```

Go (`commands/serve.go`):

```go
//goappctl:session
sess := session.New(cfg.Session, svc.DB)
svc.Session = sess
//goappctl:end
```

YAML (`config.yaml`):

```yaml
#goappctl:db
db:
  driver: sqlite3
  dsn: data/app.db
#goappctl:end
```

TS files use `//goappctl:<name>`. `frontend/package.json` is not markered — init edits it as JSON. The existing `// gen:mounts:begin/end` region in `mount_gen.go` keeps its current name (used by `gen`, not `init`).

## 6. Components (hardcoded)

| Component | Deps | Owned files/dirs (deleted when off) | Shared-file marker spots |
|---|---|---|---|
| `db` | — | `internal/service/db/`, `internal/driver/` | `serve.go`, `config.go` (`DB *db.Config`), `config.yaml` |
| `session` | — | `internal/service/session/` | `serve.go`, `config.go`, `config.yaml` |
| `admin` | session, db | `internal/controller/admin/`, `commands/admin_user.go`, `frontend/pages/admin/` | `serve.go` (admin.New/Validate/Mount), `root.go` (admin_user cmd reg, if any), `config.go`, `config.yaml`, `mount_gen.go` region |
| `ssr` | — | `frontend/ssr-*.ts`, `frontend/vite.config.ssr.ts`, SSR mode files in `server/` | `serve.go`/`server` wiring, `frontend/package.json` (JSON edit: SSR scripts + deps) |

Samples (`--no-samples`): sample site pages/resource under `internal/controller/site/` extras and `frontend/pages/` — home + health always stay.

**Extensibility plan:** when a 5th component lands (grpc/restful/buf), extract a small in-code registry (`[]Component{Name, Deps, OwnedPaths}`) from the hardcoded switch — *then*, not now.

## 7. Prerequisite template changes (land before goappctl v1)

Small PRs against the template itself:

1. Add `//goappctl:<name>` … `//goappctl:end` markers around each optional component's wiring in the shared files: `commands/serve.go`, `commands/root.go`, `internal/config/config.go`, `config.yaml`, and (SSR script/dep grouping only) `frontend/package.json`.
2. Make composition **line-oriented**: one component per line/block in `serve.go` (no `db+session+admin` crammed on one line), so whole-line marker stripping works.
3. **The linchpin — cross-component references go ONLY through `app.Services` fields (always-present, nil-able), never through another component's variable/package in a shared file.** This is what lets single-name markers work with zero alternate blocks. Concretely:
   - `serve.go` builds `svc := app.NewServices(log)` unconditionally, then each component's block *assigns into* svc: the `db` block does `svc.DB = dbSvc.DB()`, the `session` block does `svc.Session = session.New(cfg.Session, svc.DB)`. Because `svc.DB` is a plain field that is simply `nil` when the db block was stripped, the session line compiles and falls back to the memory store — no `dbSvc` reference escapes the db block.
   - Requires two small API changes: `session.New` takes `*sqldb.DB` directly (nil-tolerant) instead of the `db.Provider` interface, so it does **not** import the db component; and `Services.DB *sqldb.DB` stays a core field even when db is off (so `sqldb`, a third-party lib, remains a core go.mod dep regardless — the one accepted wart).
   - `admin` still legitimately requires db+session, so its block may freely use `svc.DB`/`svc.Session`; it is never kept without them (dependency closure guarantees it).
4. Keep `gen:mounts:begin/end` marker name unchanged.

> If this Services-mediated rule ever proves too constraining for a future component, the fallback is to re-introduce a *single, narrow* alternate-block form for that one seam — but v1 needs none.

## 8. `goappctl gen`

```
goappctl gen resource <Name> [--admin] [--no-mount]
```

- Reuses the existing `internal/scaffold` code, moved into `cmd/goappctl` (templates keep `[[ ]]` delimiters).
- Detects module path from `go.mod`; detects project shape by directory presence (no marker file).
- Emits: controller embedding `*app.Services` + `Mount(...)`, model, Vue pages into `internal/controller/<pkg>/` and `frontend/pages/<pkg>/`.
- Auto-edits the `gen:mounts` region in `internal/controller/mount_gen.go`; idempotent (re-running doesn't duplicate — the router's `ErrDuplicateRoute` / `Engine.RegistrationError()` is the runtime backstop). `--no-mount` skips the edit.
- `--admin` targets the admin area; errors clearly if `internal/controller/admin/` is absent.

## 9. Correctness (v1)

goappctl's CI runs `init` on **three combos** in temp copies of the repo and requires `go build ./...` to pass on each:

1. All-on: `--with db,session,admin,ssr`
2. Minimal: core only (no components)
3. Middle: `--with db,session,admin`

That's it — no 8-combo matrix, no boot smoke, no frontend build. Just enough to catch broken marker stripping and deletion lists.

## 10. Internal package layout

```
cmd/goappctl/
  main.go            # cobra root: init, gen, version
  internal/
    initcmd/         # pipeline steps 1–8 (selection, delete, strip, rewrite, tidy, verify)
    markers/         # find/strip //goappctl:<name> blocks (Go//YAML#/TS//), + JSON edit for package.json
    components/      # the hardcoded four: names, deps, owned paths, marker names
    scaffold/        # moved from internal/scaffold (gen)
```

Keep it flat; no interfaces until the 5th component forces the registry extraction.

## 11. Open questions / risks

- **Marker rot:** nothing enforces markers stay correct as the template evolves; the 3-combo CI is the only guard. Acceptable for v1 (a `lint-template` check is an explicit non-goal).
- **package.json JSON edit:** must preserve formatting well enough not to churn diffs — use a key-removal edit, not full re-marshal, or accept re-marshal + document it.
- **Interactive checklist dependency UX:** when the user picks admin, auto-check db/session in the UI vs. closure-after-confirm — implementer's choice, just be visible about it.
- **cgo/SSR on user machines:** stripping ssr must leave a pure-Go build; verify combo 2/3 builds without QuickJS toolchain in CI.
- **`go install @latest` skew:** a user may run a newer goappctl against an older clone. v1 answer: markers are additive and stripping unknown names is a no-op; document "use matching versions" and move on.
