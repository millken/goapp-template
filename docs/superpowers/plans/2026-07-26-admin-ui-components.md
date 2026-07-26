# Admin UI Components Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the admin area a real component vocabulary (shadcn-vue + TanStack Table) that trims away cleanly when `goappctl init` runs without `admin`.

**Architecture:** shadcn-vue component *source* is copied into `frontend/src/components/ui/`, so the bundle contains only what was copied and trimming is a path deletion. The files belong to the existing `admin` component — no new `goappctl` component. Public pages and `gen resource` templates are untouched.

**Tech Stack:** Vue 3.5 + Tailwind CSS 4 + reka-ui (primitives) + class-variance-authority + `@tanstack/vue-table`; Go 1.26 for the `goappctl` changes.

## Global Constraints

- **Scope is admin-only.** `cmd/goappctl/internal/scaffold/templates/resource/*` and `internal/controller/**` must not be modified by any task.
- **No client-side form validation.** No shadcn `form` component, no `vee-validate`. Validation is server-side via `internal/validate`.
- **Component style is `styles/default`** from `https://shadcn-vue.com/r/styles/default/<name>.json`.
- **The three real Vue flags stay mirrored** between `frontend/vite.config.ts` and `frontend/vite.config.ssr.ts`: `__VUE_OPTIONS_API__`, `__VUE_PROD_DEVTOOLS__`, `__VUE_PROD_HYDRATION_MISMATCH_DETAILS__`, all `'false'`.
- **Never server-render an open overlay.** `Dialog`/`DropdownMenu`/`Select` float their content through Teleport, which `frontend/ssr/render.ts` does not collect. Overlays must default to closed.
- **`cmd/goappctl` tests are offline.** No task may add a test that performs a network request.
- **Verification commands** used throughout: `go build ./...`, `go vet ./...`, `go test ./... -count=1`, `pnpm -C frontend type-check`, `pnpm -C frontend build`.

**Out of scope for this plan:** `goappctl gen ui <component>` (spec §6 and §12 stage 2). It gets its own plan. Until it exists, adding a component means running the upstream CLI or repeating Task 3's fetch script.

---

## Task 0: Start from a clean tree

The design was validated with a spike that left uncommitted files in the working tree. Discard them so the plan starts from committed state and every later task's verification means something.

- [ ] **Step 1: Confirm what is uncommitted**

Run: `git status --short`

Expected: modifications to `frontend/package.json`, `frontend/pnpm-lock.yaml`, `frontend/pages/admin/dashboard.vue`, `frontend/src/styles/main.css`, plus untracked `frontend/src/components/AdminLayoutShadcn.vue`, `frontend/src/components/ui/`, `frontend/src/lib/`.

- [ ] **Step 2: Discard the spike**

```bash
git checkout frontend/package.json frontend/pnpm-lock.yaml \
  frontend/pages/admin/dashboard.vue frontend/src/styles/main.css
rm -rf frontend/src/components/ui frontend/src/lib \
  frontend/src/components/AdminLayoutShadcn.vue
pnpm -C frontend install
```

- [ ] **Step 3: Verify the tree is clean and green**

Run: `git status --short && go build ./... && go vet ./... && go test ./... -count=1 && pnpm -C frontend type-check`

Expected: `git status --short` prints nothing; every command exits 0.

---

## Task 1: `.css` comment form for markers

Without this, a marker in `main.css` makes `goappctl init` fail outright — `initcmd.go` deliberately errors on markers in file types with no comment form. Task 3 puts a marker in `main.css`, so this comes first.

**Files:**
- Modify: `cmd/goappctl/internal/markers/markers.go` (the `forms` map, ~line 34)
- Test: `cmd/goappctl/internal/markers/markers_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Strip("x.css", src, opts)` handles `/*goappctl:<name>*/ … /*goappctl:end*/`. `Supported("x.css")` returns `true`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/goappctl/internal/markers/markers_test.go`:

Keep content after the block, mirroring `TestStrip_RemovesUnselectedBlock`. A
block at end-of-file instead tests the EOF edge rather than the seam, and the
blank line *before* a block is preserved — as that existing `.go` case shows with
its `package main\n\n`.

```go
func TestStrip_CSSForm(t *testing.T) {
	src := "@import \"tailwindcss\";\n\n/*goappctl:admin*/\n:root { --x: 1; }\n/*goappctl:end*/\nbody { color: red; }\n"

	got, n, err := Strip("main.css", []byte(src), opts("admin"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	if want := "@import \"tailwindcss\";\n\nbody { color: red; }\n"; string(got) != want {
		t.Errorf("admin off: got %q, want %q", got, want)
	}

	got, n, err = Strip("main.css", []byte(src), opts())
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 0 {
		t.Errorf("stripped = %d, want 0", n)
	}
	want := "@import \"tailwindcss\";\n\n:root { --x: 1; }\nbody { color: red; }\n"
	if string(got) != want {
		t.Errorf("admin on: got %q, want %q", got, want)
	}
}

func TestSupported_CSS(t *testing.T) {
	if !Supported("frontend/src/styles/main.css") {
		t.Error("Supported(.css) = false, want true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/goappctl/internal/markers/ -run 'CSSForm|Supported_CSS' -v -count=1`

Expected: FAIL. `TestSupported_CSS` fails outright; `TestStrip_CSSForm` fails because an unsupported extension is a no-op, so the source comes back unchanged.

- [ ] **Step 3: Add the comment form**

In `cmd/goappctl/internal/markers/markers.go`, add one entry to the `forms` map, after `".ts"`:

```go
	".css":  {open: "/*goappctl:", end: "/*goappctl:end*/", close: "*/"},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/goappctl/internal/markers/ -count=1`

Expected: PASS, all tests in the package.

- [ ] **Step 5: Commit**

```bash
git add cmd/goappctl/internal/markers/markers.go cmd/goappctl/internal/markers/markers_test.go
git commit -m "feat(goappctl): recognise goappctl markers in .css files

The admin theme lives in frontend/src/styles/main.css and has to disappear when
admin is trimmed. init deliberately errors on markers in file types with no
comment form, so the block needs a real form rather than a special case."
```

---

## Task 2: Prune admin-only npm dependencies on trim

`initcmd` already rewrites `frontend/package.json` when `ssr` is off, via `markers.StripSSRScripts`. This adds the sibling for `admin`. Both need the same trailing-comma repair, so that loop moves into a shared helper.

**Files:**
- Modify: `cmd/goappctl/internal/markers/markers.go` (add `StripAdminDeps`, extract the comma helper from `StripSSRScripts`)
- Modify: `cmd/goappctl/internal/initcmd/initcmd.go` (add `stripAdminDeps`, call it)
- Test: `cmd/goappctl/internal/markers/markers_test.go`, `cmd/goappctl/internal/initcmd/initcmd_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `markers.StripAdminDeps(src []byte) ([]byte, error)` — removes the eight admin-only dependency lines from a `package.json`, returns the input unchanged when none are present, and never produces invalid JSON.

- [ ] **Step 1: Write the failing markers test**

Append to `cmd/goappctl/internal/markers/markers_test.go`:

```go
func TestStripAdminDeps(t *testing.T) {
	src := []byte(`{
  "dependencies": {
    "@tanstack/vue-table": "^8.21.3",
    "@vueuse/core": "^13.0.0",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "lucide-vue-next": "^0.544.0",
    "reka-ui": "^2.10.1",
    "tailwind-merge": "^3.3.1",
    "vue": "^3.5.40"
  },
  "devDependencies": {
    "tw-animate-css": "^1.4.0",
    "vite": "^8.1.5"
  }
}
`)

	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	for _, gone := range []string{
		"@tanstack/vue-table", "@vueuse/core", "class-variance-authority",
		"clsx", "lucide-vue-next", "reka-ui", "tailwind-merge",
	} {
		if _, ok := pkg.Dependencies[gone]; ok {
			t.Errorf("dependencies still has %q", gone)
		}
	}
	if _, ok := pkg.DevDependencies["tw-animate-css"]; ok {
		t.Error("devDependencies still has tw-animate-css")
	}
	if pkg.Dependencies["vue"] != "^3.5.40" {
		t.Errorf("vue was not preserved: %q", pkg.Dependencies["vue"])
	}
	if pkg.DevDependencies["vite"] != "^8.1.5" {
		t.Errorf("vite was not preserved: %q", pkg.DevDependencies["vite"])
	}
}

func TestStripAdminDeps_Idempotent(t *testing.T) {
	src := []byte("{\n  \"dependencies\": {\n    \"vue\": \"^3.5.40\"\n  }\n}\n")
	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}
	if string(out) != string(src) {
		t.Errorf("a package.json with no admin deps must be untouched:\ngot  %q\nwant %q", out, src)
	}
}

// The fixture above is realistic — pnpm sorts keys, so "vue" ends up last in
// dependencies and no admin dep is ever the final key. That means it never
// exercises the trailing-comma repair, and would pass even if
// dropCommaBeforeBrace were never called. The first test below forces that
// repair; the second covers an emptied block, which is valid to assert but does
// not exercise the repair, since a lone key never carries a comma.
func TestStripAdminDeps_LastKeyRemovedStaysValidJSON(t *testing.T) {
	src := []byte("{\n  \"dependencies\": {\n    \"vue\": \"^3.5.40\",\n    \"reka-ui\": \"^2.10.1\"\n  }\n}\n")

	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("removing the final key left invalid JSON: %v\n%s", err, out)
	}
	if len(pkg.Dependencies) != 1 || pkg.Dependencies["vue"] != "^3.5.40" {
		t.Errorf("want only vue to survive, got %v", pkg.Dependencies)
	}
}

func TestStripAdminDeps_EmptiedBlockStaysValidJSON(t *testing.T) {
	src := []byte("{\n  \"devDependencies\": {\n    \"tw-animate-css\": \"^1.4.0\"\n  }\n}\n")

	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}
	var pkg struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("emptying a block left invalid JSON: %v\n%s", err, out)
	}
	if len(pkg.DevDependencies) != 0 {
		t.Errorf("want an empty devDependencies, got %v", pkg.DevDependencies)
	}
}
```

**Sanity-check that these tests can fail.** After they pass, temporarily comment out the `dropCommaBeforeBrace(out)` call in `StripAdminDeps` and re-run: `TestStripAdminDeps_LastKeyRemovedStaysValidJSON` must fail with an invalid-JSON error. Restore the call. A test that cannot fail is not coverage.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/goappctl/internal/markers/ -run StripAdminDeps -v -count=1`

Expected: FAIL to compile — `undefined: StripAdminDeps`.

- [ ] **Step 3: Extract the comma helper and add `StripAdminDeps`**

In `cmd/goappctl/internal/markers/markers.go`, replace the trailing-comma loop at the end of `StripSSRScripts` (currently `for i := 0; i+1 < len(out); i++ { … }`) with a call:

```go
	dropCommaBeforeBrace(out)
	return []byte(strings.Join(out, "\n") + trailer), nil
}

// dropCommaBeforeBrace repairs the JSON that removing a key can leave behind: a
// trailing comma on the line before a closing brace. Mutates out in place.
func dropCommaBeforeBrace(out []string) {
	for i := 0; i+1 < len(out); i++ {
		next := strings.TrimSpace(out[i+1])
		if !strings.HasPrefix(next, "}") {
			continue
		}
		if cur := strings.TrimRight(out[i], " \t"); strings.HasSuffix(cur, ",") {
			out[i] = strings.TrimSuffix(cur, ",")
		}
	}
}

// adminDeps are the packages that exist only for the copied shadcn components,
// listed as they appear as JSON keys.
var adminDeps = []string{
	"@tanstack/vue-table",
	"@vueuse/core",
	"class-variance-authority",
	"clsx",
	"lucide-vue-next",
	"reka-ui",
	"tailwind-merge",
	"tw-animate-css",
}

// StripAdminDeps removes the admin-only packages from a package.json. It is
// line-based like StripSSRScripts: the file is developer-edited, so reformatting
// it through a JSON round-trip would produce a needlessly large diff.
func StripAdminDeps(src []byte) ([]byte, error) {
	body, trailer := string(src), ""
	if strings.HasSuffix(body, "\n") {
		body, trailer = strings.TrimSuffix(body, "\n"), "\n"
	}

	drop := make(map[string]bool, len(adminDeps))
	for _, d := range adminDeps {
		drop[`"`+d+`"`] = true
	}

	var out []string
	for _, line := range strings.Split(body, "\n") {
		key, _, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && drop[strings.TrimSpace(key)] {
			continue
		}
		out = append(out, line)
	}

	dropCommaBeforeBrace(out)
	return []byte(strings.Join(out, "\n") + trailer), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/goappctl/internal/markers/ -count=1`

Expected: PASS. `TestStripSSRScripts*` must still pass — the extraction changed no behaviour.

- [ ] **Step 5: Wire it into initcmd**

In `cmd/goappctl/internal/initcmd/initcmd.go`, add below `stripSSRScripts`:

```go
// stripAdminDeps removes the packages that exist only for the copied shadcn
// components. They cost no bundle bytes when unimported, but a trimmed project
// should not download them.
func stripAdminDeps(o Options) error {
	rel := "frontend/package.json"
	full := filepath.Join(o.Root, rel)
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out, err := markers.StripAdminDeps(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if bytes.Equal(out, src) {
		return nil
	}
	fmt.Fprintf(o.Out, "  edit %s (drop admin-only dependencies)\n", rel)
	if o.DryRun {
		return nil
	}
	return os.WriteFile(full, out, 0o644)
}
```

Then add the call in `Run`, immediately after the existing step 4a block (around line 132):

```go
	// step 4a: package.json scripts, only when ssr is off.
	if off["ssr"] {
		if err := stripSSRScripts(o); err != nil {
			return err
		}
	}

	// step 4b: package.json dependencies, only when admin is off.
	if off["admin"] {
		if err := stripAdminDeps(o); err != nil {
			return err
		}
	}
```

- [ ] **Step 6: Do not add an initcmd-level test yet**

Deliberately no test in `initcmd` for this task. The end-to-end assertion belongs in `TestRun_Combos`, but it can only be meaningful once the eight packages actually exist in `frontend/package.json` — before that, an "absent" assertion passes because nothing was ever there, and a "present" assertion simply fails. Task 3 adds both sides together, in the same task that adds the packages.

What is covered here: `StripAdminDeps` against fixture JSON (Step 1), which is the whole of the logic. What is not: the `if off["admin"]` wiring, until Task 3.

Do **not** add an ungated test that calls `Run` — `Run` ends with `go build`/`vet`/`test`, which is why `TestRun_Combos` is gated behind `GOAPPCTL_E2E` in the first place.

- [ ] **Step 7: Run the suite**

```bash
go test ./cmd/goappctl/... -count=1
GOAPPCTL_E2E=1 go test ./cmd/goappctl/internal/initcmd/ -run TestRun_Combos -count=1
```

Expected: both PASS. The gated run must stay green — this task changes what `init` does only when `admin` is off and the admin packages are present, which is not yet any case in the matrix.

- [ ] **Step 8: Commit**

```bash
git add cmd/goappctl/internal/markers cmd/goappctl/internal/initcmd
git commit -m "feat(goappctl): prune admin-only frontend deps when admin is trimmed

Sibling of stripSSRScripts. The eight packages behind the copied shadcn
components cost zero bundle bytes when nothing imports them, but a trimmed
project should not pnpm-install what it can never use.

The trailing-comma repair both editors need moves into dropCommaBeforeBrace."
```

---

## Task 3: Copy the component set, dependencies and theme

Brings in the twelve components plus `lib/utils.ts`, their npm dependencies, and the trimmable theme block. Nothing renders differently yet — later tasks consume these.

**Files:**
- Create: `frontend/src/components/ui/**` (12 components, ~78 files)
- Create: `frontend/src/lib/utils.ts`
- Modify: `frontend/package.json`, `frontend/pnpm-lock.yaml`
- Modify: `frontend/src/styles/main.css`

**Interfaces:**
- Consumes: Task 1's `.css` marker form.
- Produces: `@/components/ui/{alert,badge,button,card,dialog,dropdown-menu,input,label,pagination,select,separator,table}` and `@/lib/utils` exporting `cn(...inputs)` and `valueUpdater(updaterOrValue, ref)`. Tailwind utilities `bg-background`, `text-foreground`, `bg-primary`, `border-input`, `ring-ring` resolve.

- [ ] **Step 1: Write the fetch script**

Create `/tmp/fetch-shadcn.mjs` (a one-shot bootstrap, not a repo file — Task 12's follow-up plan replaces it with `goappctl gen ui`):

```javascript
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'

const ROOT = process.argv[2]              // absolute path to frontend/src
const BASE = 'https://shadcn-vue.com/r/styles/default'
const want = process.argv.slice(3)

const seen = new Set(), npmDeps = new Set(), written = []

async function item(name) {
  if (seen.has(name)) return
  seen.add(name)
  const res = await fetch(`${BASE}/${name}.json`)
  if (!res.ok) throw new Error(`${name}: HTTP ${res.status}`)
  const j = await res.json()
  for (const d of j.dependencies ?? []) npmDeps.add(d)
  for (const f of j.files ?? []) {
    const rel = f.path ?? f.name
    const dest = rel.startsWith('ui/') ? join(ROOT, 'components', rel) : join(ROOT, rel)
    await mkdir(dirname(dest), { recursive: true })
    await writeFile(dest, f.content ?? '')
    written.push(dest)
  }
  for (const d of j.registryDependencies ?? []) await item(d)
}

for (const n of want) await item(n)
console.log(`wrote ${written.length} files`)
console.log(`npm deps declared: ${[...npmDeps].sort().join(' ')}`)
```

- [ ] **Step 2: Fetch the twelve components**

```bash
node /tmp/fetch-shadcn.mjs "$PWD/frontend/src" \
  alert badge button card dialog dropdown-menu input label \
  pagination select separator table utils
```

Expected: `wrote 78 files` (±a few if upstream changed), and a declared-dependency line.

- [ ] **Step 3: Rewrite registry-internal import paths**

The registry ships `@/registry/default/ui/...` imports that the upstream CLI rewrites from `components.json`. Skipping this produces code that will not build — `Pagination*.vue` imports `buttonVariants` that way.

```bash
grep -rl "@/registry/default/ui" frontend/src/components/ui \
  | xargs sed -i 's|@/registry/default/ui|@/components/ui|g'
grep -r "@/registry" frontend/src/components/ui frontend/src/lib | wc -l
```

Expected: the final count is `0`.

- [ ] **Step 4: Install the dependencies**

The registry's declared list is incomplete — the components also import `lucide-vue-next` and `class-variance-authority`. Install what they actually import:

```bash
pnpm -C frontend add reka-ui @vueuse/core lucide-vue-next \
  class-variance-authority clsx tailwind-merge @tanstack/vue-table
pnpm -C frontend add -D tw-animate-css
```

- [ ] **Step 5: Verify the installed set matches Task 2's `adminDeps`**

Run: `node -e "const p=require('./frontend/package.json');console.log(Object.keys({...p.dependencies,...p.devDependencies}).sort().join(' '))"`

Expected: the output contains every name in `markers.adminDeps` (Task 2). If a name differs, fix `adminDeps` — the pruning list and the installed list must agree, or trimming silently leaves a package behind.

- [ ] **Step 6: Add the theme block to main.css**

Fetch the theme values and generate the file:

```bash
curl -sS 'https://shadcn-vue.com/init?base=reka&style=nova&baseColor=neutral&theme=neutral&iconLibrary=lucide&font=geist-sans&rtl=false&menuAccent=subtle&menuColor=default&radius=default&template=vite&track=1' \
  -o /tmp/shadcn-init.json

node -e '
const fs = require("fs");
const j = JSON.parse(fs.readFileSync("/tmp/shadcn-init.json", "utf8"));
const block = (sel, vars) =>
  sel + " {\n" + Object.entries(vars).map(([k, v]) => "  --" + k + ": " + v + ";").join("\n") + "\n}\n";
const themeInline =
  "@theme inline {\n" +
  Object.keys(j.cssVars.light).map((k) => "  --color-" + k + ": var(--" + k + ");").join("\n") +
  "\n  --radius: 0.5rem;\n}\n";
fs.writeFileSync("frontend/src/styles/main.css", [
  `@import "tailwindcss";`,
  "",
  "/*goappctl:admin*/",
  `@import "tw-animate-css";`,
  "",
  "@custom-variant dark (&:is(.dark *));",
  "",
  block(":root", j.cssVars.light),
  block(".dark", j.cssVars.dark),
  themeInline,
  "/*goappctl:end*/",
].join("\n"));
'
head -8 frontend/src/styles/main.css
```

Expected: `@import "tailwindcss";` on line 1 **outside** the marker, then `/*goappctl:admin*/`, then the theme.

- [ ] **Step 7: Verify the theme block strips cleanly**

Run: `go test ./cmd/goappctl/... -count=1`

Expected: PASS. In particular `init`'s marker walk must not error on `main.css` — that is Task 1 doing its job. If it errors with "no comment form", Task 1 was not applied.

- [ ] **Step 8: Verify the frontend builds and type-checks**

Run: `pnpm -C frontend type-check && pnpm -C frontend build`

Expected: both exit 0. `type-check` runs `vue-tsc` under `strict: true` across all ~78 copied files.

- [ ] **Step 9: Close Task 2's coverage gap in the trimming matrix**

Task 2 added `markers.StripAdminDeps` and its `if off["admin"]` wiring but could not test the wiring end to end, because the packages did not exist. They do now, so both sides of the assertion become meaningful — add them here.

In `cmd/goappctl/internal/initcmd/initcmd_test.go`, add two fields to the `TestRun_Combos` case struct:

```go
		// wantFrontendAbsent / wantFrontendPresent are checked against
		// frontend/package.json: the admin-only UI packages must leave with the
		// admin component, and nothing else may.
		wantFrontendAbsent  []string
		wantFrontendPresent []string
```

Set them on two existing cases:

```go
		{
			name: "all-on", with: []string{"db", "session", "admin", "ssr"},
			wantPresent:         []string{"mattn/go-sqlite3", "buke/quickjs-go"},
			wantFrontendPresent: []string{"reka-ui", "@tanstack/vue-table", "tw-animate-css"},
		},
		{
			name: "minimal", with: nil,
			wantAbsent:          []string{"mattn/go-sqlite3", "buke/quickjs-go"},
			wantFrontendAbsent:  []string{"reka-ui", "@tanstack/vue-table", "tw-animate-css"},
			wantFrontendPresent: []string{`"vue"`},
		},
```

And assert inside the subtest, after the existing `go.mod` assertions:

```go
			pkg, err := os.ReadFile(filepath.Join(root, "frontend/package.json"))
			if err != nil {
				t.Fatalf("read frontend/package.json: %v", err)
			}
			for _, gone := range c.wantFrontendAbsent {
				if strings.Contains(string(pkg), gone) {
					t.Errorf("frontend/package.json still lists %q:\n%s", gone, pkg)
				}
			}
			for _, kept := range c.wantFrontendPresent {
				if !strings.Contains(string(pkg), kept) {
					t.Errorf("frontend/package.json lost %q:\n%s", kept, pkg)
				}
			}
```

- [ ] **Step 10: Run the gated matrix**

Run: `GOAPPCTL_E2E=1 go test ./cmd/goappctl/internal/initcmd/ -run TestRun_Combos -count=1`

Expected: PASS, all four cases. This is the first run where `all-on` proves the packages survive and `minimal` proves they are pruned. Slow — four full init plus build/vet/test cycles.

- [ ] **Step 11: Commit**

```bash
git add frontend/src/components/ui frontend/src/lib frontend/src/styles/main.css \
  frontend/package.json frontend/pnpm-lock.yaml \
  cmd/goappctl/internal/initcmd/initcmd_test.go
git commit -m "feat(frontend): copy the shadcn-vue component set for the admin area

Twelve components plus lib/utils, source-copied rather than depended on, so the
bundle holds only what is used and trimming is a path deletion. Registry-internal
@/registry/default/ui imports are rewritten to @/components/ui, which the
upstream CLI does from components.json and a manual fetch must do by hand.

The theme lives inside a goappctl:admin block so it leaves with the admin area.
No shadcn form component: validation is server-side (internal/validate)."
```

---

## Task 4: Hand the copied files to the `admin` component

**Files:**
- Modify: `cmd/goappctl/internal/components/components.go` (the `admin` entry, ~line 41)
- Test: `cmd/goappctl/internal/components/components_test.go`

**Interfaces:**
- Consumes: the paths Task 3 created.
- Produces: `components.Get("admin").Owned` includes `frontend/src/components/ui` and `frontend/src/lib`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/goappctl/internal/components/components_test.go`:

```go
func TestAdminOwnsCopiedUIComponents(t *testing.T) {
	c, ok := Get("admin")
	if !ok {
		t.Fatal("admin component missing")
	}
	for _, want := range []string{"frontend/src/components/ui", "frontend/src/lib"} {
		if !slices.Contains(c.Owned, want) {
			t.Errorf("admin.Owned missing %q; a trimmed project would ship the shadcn source", want)
		}
	}
}
```

Add `"slices"` to that file's imports if it is not already there.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/goappctl/internal/components/ -run TestAdminOwnsCopiedUIComponents -v -count=1`

Expected: FAIL, both paths reported missing.

- [ ] **Step 3: Add the two paths**

In `cmd/goappctl/internal/components/components.go`, extend the `admin` entry's `Owned` slice:

```go
		Owned: []string{
			"internal/controller/admin",
			"commands/admin_user.go",
			"frontend/pages/admin",
			"frontend/src/components/AdminLayout.vue",
			"frontend/src/components/ui",
			"frontend/src/lib",
		},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/goappctl/... -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/goappctl/internal/components
git commit -m "feat(goappctl): admin owns the copied shadcn component source

frontend/src/components/ui and frontend/src/lib exist only for the admin area,
so a project trimmed without admin should not carry them."
```

---

## Task 5: Delete the dead `__VUE_FEATURE_*` defines

Five defines that nothing reads. Verified during design: no package under `node_modules` references `__VUE_FEATURE_`, and flipping them produced byte-identical SSR HTML (17489 bytes) and an identical client bundle. They are worse than inert — they read as though Teleport were disabled deliberately, which misleads anyone reasoning about overlay components.

**Files:**
- Modify: `frontend/vite.config.ts`
- Modify: `frontend/vite.config.ssr.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. A pure deletion.

- [ ] **Step 1: Record the current bundle sizes**

```bash
pnpm -C frontend build >/dev/null 2>&1
for f in frontend/dist/assets/*.js frontend/dist/ssr-render-cjs.js; do
  printf '%s %s\n' "$(gzip -c "$f" | wc -c)" "$(basename "$f")"
done | sort > /tmp/sizes-before.txt
cat /tmp/sizes-before.txt
```

- [ ] **Step 2: Confirm nothing reads the flags**

Run: `grep -rl "__VUE_FEATURE_" frontend/node_modules/.pnpm/ | head -3`

Expected: no output. If any package does reference them, **stop** — the premise of this task is wrong and the spec needs revisiting.

- [ ] **Step 3: Delete the five lines from both configs**

```bash
sed -i "/__VUE_FEATURE_SUSPENSE__/d; /__VUE_FEATURE_TELEPORT__/d; \
        /__VUE_FEATURE_TRANSITION__/d; /__VUE_FEATURE_KEEP_ALIVE__/d; \
        /__VUE_FEATURE_SCOPED_SLOT__/d" \
  frontend/vite.config.ts frontend/vite.config.ssr.ts
grep -c "__VUE_" frontend/vite.config.ts frontend/vite.config.ssr.ts
```

Expected: `3` for each file — the three real flags remain.

- [ ] **Step 4: Verify the three real flags are still mirrored**

Run: `diff <(grep "__VUE_" frontend/vite.config.ts) <(grep "__VUE_" frontend/vite.config.ssr.ts) && echo MIRRORED`

Expected: `MIRRORED`.

- [ ] **Step 5: Verify the build is byte-identical**

```bash
pnpm -C frontend build >/dev/null 2>&1
for f in frontend/dist/assets/*.js frontend/dist/ssr-render-cjs.js; do
  printf '%s %s\n' "$(gzip -c "$f" | wc -c)" "$(basename "$f")"
done | sort > /tmp/sizes-after.txt
diff /tmp/sizes-before.txt /tmp/sizes-after.txt && echo "IDENTICAL"
```

Expected: `IDENTICAL`. Content-hashed filenames mean any real change shows up here. If sizes differ, the flags were doing something — stop and re-open the spec.

- [ ] **Step 6: Commit**

```bash
git add frontend/vite.config.ts frontend/vite.config.ssr.ts
git commit -m "refactor(frontend): drop five Vue feature defines nothing reads

Vue 3.5.40's runtime references only __VUE_OPTIONS_API__, __VUE_PROD_DEVTOOLS__
and __VUE_PROD_HYDRATION_MISMATCH_DETAILS__; no package under node_modules
mentions __VUE_FEATURE_ at all. Flipping TELEPORT/TRANSITION produced a
byte-identical SSR bundle and client build, and removing all five leaves both
byte-identical again.

They were misleading rather than harmless: they read as though Teleport were off
on purpose, which matters now that overlay components are arriving. The mirroring
invariant from 253acad still holds for the three real flags."
```

---

## Task 6: Rebuild `AdminLayout.vue` on the components

Rewritten in place. A second parallel layout would only drift.

**Files:**
- Modify: `frontend/src/components/AdminLayout.vue`

**Interfaces:**
- Consumes: `@/components/ui/{alert,button,separator}`, `lucide-vue-next`.

**First, add a `success` variant to the Alert.** Upstream shadcn ships only
`default` and `destructive`, but the shell distinguishes three flash kinds — the
hand-written version this replaces gave success green, error red, other grey.
Losing that is a regression in a shipped feature. The copied component is ours, so
extend it: in `frontend/src/components/ui/alert/index.ts`, add a third entry
beside `default` and `destructive`, leaving the base class string and
`defaultVariants` alone:

```ts
        success:
          "border-emerald-500/50 text-emerald-700 dark:border-emerald-500 dark:text-emerald-400 [&>svg]:text-emerald-600",
```
- Produces: unchanged props — `menu?: MenuItem[]`, `user?: unknown`, `mount?: string`, `loginPath?: string`, `flash?: Record<string, string>` — and a default slot. Every admin page keeps working without edits.

- [ ] **Step 1: Replace the file**

```vue
<script setup lang="ts">
// Shared admin shell: sidebar nav (built from the server-provided menu) + a
// logout form, with page content in the default slot. Menu items are registered
// by admin resource modules (AddMenuItem) and injected as the `adminMenu` prop
// by the admin auth middleware.
//
// Navigation stays plain <a href>: Button renders an anchor via `as`, and the
// PJAX layer intercepts those through document-level delegation, so there is no
// router integration to wire.
import { computed } from 'vue'
import { LogOut } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'

interface MenuItem {
  title: string
  path: string
  order?: number
}

// `user` and `loginPath` are unused here but declared so Vue treats them as
// props rather than leaking them onto the root element as attributes.
const props = defineProps<{
  menu?: MenuItem[]
  user?: unknown
  mount?: string
  loginPath?: string
  flash?: Record<string, string>
}>()

const base = computed(() => props.mount || '/admin')

// One-shot messages staged by the server before a redirect (sess.Flash), keyed
// by kind. The session middleware consumes them, so they vanish on the next
// navigation — no dismiss button needed.
const flashVariant = (kind: string) =>
  kind === 'error' ? 'destructive' : kind === 'success' ? 'success' : 'default'
</script>

<template>
  <div class="min-h-screen flex bg-muted/40">
    <aside class="w-56 border-r bg-background p-4 flex flex-col">
      <div class="text-lg font-semibold mb-4">Admin</div>
      <Separator />
      <nav class="flex-1 space-y-1 py-4">
        <Button as="a" :href="base" variant="ghost" class="w-full justify-start">
          Dashboard
        </Button>
        <Button
          v-for="item in menu || []"
          :key="item.path"
          as="a"
          :href="item.path"
          variant="ghost"
          class="w-full justify-start"
        >{{ item.title }}</Button>
      </nav>
      <form :action="`${base}/logout`" method="post">
        <Button type="submit" variant="outline" size="sm" class="w-full">
          <LogOut />
          Log out
        </Button>
      </form>
    </aside>

    <main class="flex-1 p-8">
      <Alert
        v-for="(message, kind) in flash || {}"
        :key="kind"
        :variant="flashVariant(kind)"
        class="mb-4"
      >
        <AlertDescription>{{ message }}</AlertDescription>
      </Alert>
      <slot />
    </main>
  </div>
</template>
```

- [ ] **Step 2: Verify it type-checks and builds**

Run: `pnpm -C frontend type-check && pnpm -C frontend build`

Expected: both exit 0.

- [ ] **Step 3: Verify the shell reaches the SSR bundle**

Run: `pnpm -C frontend build:ssr && grep -c "Log out" frontend/dist/ssr-render-cjs.js`

Expected: `1` or more. Task 11 turns this into a real render test; here it only confirms the rewritten layout is being bundled at all.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/AdminLayout.vue
git commit -m "feat(frontend): rebuild the admin shell on shadcn components

Same props, same contract, same path — rewritten in place rather than added
alongside, so there is no second layout to drift. Navigation is still plain
anchors, which the PJAX layer already intercepts."
```

---

## Task 7: Rebuild `login.vue`

**Files:**
- Modify: `frontend/pages/admin/login.vue`

**Interfaces:**
- Consumes: `@/components/ui/{card,label,input,button,alert}`.
- Produces: unchanged props `loginPath?: string`, `error?: string`; still a plain form POST.

- [ ] **Step 1: Replace the file**

```vue
<script setup lang="ts">
// Admin login. A plain HTML form POST (not an Inertia visit): the server
// authenticates, sets the session cookie, and 302-redirects to the dashboard —
// so this page needs no client-side auth logic. `loginPath` and `error` are
// provided by the admin module's handlers.
//
// `error` is the form-level channel (bad credentials). Per-field messages use
// the `errors` prop instead; the two coexist and login only needs this one.
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

defineProps<{
  loginPath?: string
  error?: string
}>()
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-muted/40">
    <Card class="w-80">
      <CardHeader>
        <CardTitle>Admin sign in</CardTitle>
      </CardHeader>
      <CardContent>
        <form :action="loginPath || '/admin/login'" method="post" class="space-y-4">
          <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
          </Alert>
          <div class="space-y-1.5">
            <Label for="username">Username</Label>
            <Input id="username" name="username" autocomplete="username" required />
          </div>
          <div class="space-y-1.5">
            <Label for="password">Password</Label>
            <Input
              id="password"
              name="password"
              type="password"
              autocomplete="current-password"
              required
            />
          </div>
          <Button type="submit" class="w-full">Sign in</Button>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
```

- [ ] **Step 2: Verify the admin handler tests still pass**

Run: `go test ./internal/controller/admin/ -count=1 && pnpm -C frontend type-check`

Expected: PASS and exit 0. The Go tests assert on props and redirects, not markup, so they must be unaffected — if one fails, the prop contract was changed by mistake.

- [ ] **Step 3: Commit**

```bash
git add frontend/pages/admin/login.vue
git commit -m "feat(frontend): rebuild the admin login page on shadcn components

Same props and the same plain form POST; Card/Label/Input/Button replace the
hand-rolled Tailwind. The form-level \`error\` channel is unchanged."
```

---

## Task 8: Restyle `dashboard.vue`

Stays a dashboard — the table demo belongs in the generated templates, not here.

**Files:**
- Modify: `frontend/pages/admin/dashboard.vue`

**Interfaces:**
- Consumes: `@/components/ui/card`, `AdminLayout`.
- Produces: unchanged props.

- [ ] **Step 1: Replace the file**

```vue
<script setup lang="ts">
import AdminLayout from '@/components/AdminLayout.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number }

// Shared props injected by the admin auth middleware on authenticated requests.
defineProps<{
  adminMenu?: MenuItem[]
  adminUser?: unknown
  adminMount?: string
  loginPath?: string
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminLayout
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :flash="flash"
  >
    <Card>
      <CardHeader>
        <CardTitle>Dashboard</CardTitle>
        <CardDescription>Signed in as {{ adminUser }}.</CardDescription>
      </CardHeader>
      <CardContent class="text-sm text-muted-foreground">
        Generate an admin resource with <code>goappctl gen admin &lt;name&gt;</code>;
        it registers itself in the menu on the left.
      </CardContent>
    </Card>
  </AdminLayout>
</template>
```

- [ ] **Step 2: Verify**

Run: `pnpm -C frontend type-check && pnpm -C frontend build`

Expected: both exit 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/pages/admin/dashboard.vue
git commit -m "feat(frontend): restyle the admin dashboard with Card"
```

---

## Task 9: `gen admin` index template — table, row actions, pagination

The `<ul>` becomes a real list page. `TestAdmin_OutputCompiles` already builds generated output, so a template that produces broken Go or Vue fails the suite.

**Files:**
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl`
- Test: `cmd/goappctl/internal/scaffold/admin_test.go`

**Interfaces:**
- Consumes: the component set; `@tanstack/vue-table`; `valueUpdater` from `@/lib/utils`.
- Produces: unchanged props `items`, `basePath`, plus the admin shell props. Handlers are not touched.

- [ ] **Step 1: Replace the template**

Template delimiters are `[[` / `]]`.

```vue
<script setup lang="ts">
import { h, ref } from 'vue'
import {
  FlexRender, getCoreRowModel, getFilteredRowModel, getPaginationRowModel,
  getSortedRowModel, useVueTable,
  type ColumnDef, type ColumnFiltersState, type SortingState,
} from '@tanstack/vue-table'
import { ArrowUpDown, MoreHorizontal, Plus } from 'lucide-vue-next'
import AdminLayout from '@/components/AdminLayout.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Pagination, PaginationContent, PaginationEllipsis, PaginationFirst,
  PaginationItem, PaginationLast, PaginationNext, PaginationPrevious,
} from '@/components/ui/pagination'
import {
  Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { valueUpdater } from '@/lib/utils'

interface MenuItem { title: string; path: string; order?: number }
interface [[.Type]] { id: number; name: string }

const props = defineProps<{
  items: [[.Type]][]
  basePath: string
  // Injected by the admin auth middleware for the shell:
  adminMenu?: MenuItem[]
  adminUser?: unknown
  adminMount?: string
  loginPath?: string
  // One-shot messages staged by the handlers before their redirects:
  flash?: Record<string, string>
}>()

// Rows per page. Both the table's row model and the pager read this, so they
// cannot drift apart.
const PAGE_SIZE = 20

// Sorting, filtering and paging all happen client-side over `items`. Swap in
// server-side paging by adding query params to the handler and setting
// manualPagination: true here.
const sorting = ref<SortingState>([])
const columnFilters = ref<ColumnFiltersState>([])

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<[[.Type]] | null>(null)

const columns: ColumnDef<[[.Type]]>[] = [
  { accessorKey: 'id', header: 'ID' },
  {
    accessorKey: 'name',
    header: ({ column }) =>
      h(
        Button,
        {
          variant: 'ghost',
          class: '-ml-4',
          onClick: () => column.toggleSorting(column.getIsSorted() === 'asc'),
        },
        () => ['Name', h(ArrowUpDown, { class: 'ml-2 size-4' })],
      ),
  },
]

const table = useVueTable({
  get data() { return props.items },
  columns,
  getCoreRowModel: getCoreRowModel(),
  getSortedRowModel: getSortedRowModel(),
  getFilteredRowModel: getFilteredRowModel(),
  getPaginationRowModel: getPaginationRowModel(),
  onSortingChange: (u) => valueUpdater(u, sorting),
  onColumnFiltersChange: (u) => valueUpdater(u, columnFilters),
  initialState: { pagination: { pageSize: PAGE_SIZE } },
  state: {
    get sorting() { return sorting.value },
    get columnFilters() { return columnFilters.value },
  },
})

const setNameFilter = (v: string | number) =>
  table.getColumn('name')?.setFilterValue(String(v))
</script>

<template>
  <AdminLayout
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :flash="flash"
  >
    <Card>
      <CardHeader class="flex flex-row items-center justify-between">
        <CardTitle>[[.Type]]</CardTitle>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          New
        </Button>
      </CardHeader>

      <CardContent class="space-y-4">
        <Input
          placeholder="Filter by name…"
          class="max-w-xs"
          @update:model-value="setNameFilter"
        />

        <Table>
          <TableHeader>
            <TableRow v-for="hg in table.getHeaderGroups()" :key="hg.id">
              <TableHead v-for="header in hg.headers" :key="header.id">
                <FlexRender
                  :render="header.column.columnDef.header"
                  :props="header.getContext()"
                />
              </TableHead>
              <TableHead class="w-12" />
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="row in table.getRowModel().rows" :key="row.id">
              <TableCell v-for="cell in row.getVisibleCells()" :key="cell.id">
                <FlexRender :render="cell.column.columnDef.cell" :props="cell.getContext()" />
              </TableCell>
              <TableCell>
                <DropdownMenu>
                  <DropdownMenuTrigger as-child>
                    <Button variant="ghost" size="icon-sm"><MoreHorizontal /></Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem as="a" :href="`${basePath}/${row.original.id}/edit`">
                      Edit
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      variant="destructive"
                      @select="pending = row.original"
                    >Delete</DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </TableCell>
            </TableRow>
            <TableEmpty
              v-if="!table.getRowModel().rows.length"
              :colspan="columns.length + 1"
            >
              No [[.Table]] yet.
            </TableEmpty>
          </TableBody>
        </Table>

        <!-- Controlled by the table, not by reka-ui's own page state: it emits
             update:page, TanStack owns the index. PaginationItem is for numbered
             pages — wrapping Prev/Next in one nests a <button> inside a <button>,
             which browsers reparse and hydration then disagrees with. -->
        <!-- Page state lives in the table: reka-ui emits update:page and we
             forward it, so there is one source of truth. First/Previous/Next/Last
             are siblings of the page items, never parents — wrapping one in a
             PaginationItem nests a <button> inside a <button>, which browsers
             reparse and hydration then disagrees with. PaginationItem is itself
             a button (it applies buttonVariants), so the page number goes in its
             slot rather than in a nested Button. -->
        <Pagination
          v-if="table.getPageCount() > 1"
          :items-per-page="PAGE_SIZE"
          :total="table.getFilteredRowModel().rows.length"
          :page="table.getState().pagination.pageIndex + 1"
          :sibling-count="1"
          show-edges
          @update:page="(p) => table.setPageIndex(p - 1)"
        >
          <PaginationContent v-slot="{ items }" class="justify-end">
            <PaginationFirst />
            <PaginationPrevious />
            <template v-for="(item, i) in items">
              <PaginationItem
                v-if="item.type === 'page'"
                :key="`page-${item.value}`"
                :value="item.value"
                :is-active="item.value === table.getState().pagination.pageIndex + 1"
              >{{ item.value }}</PaginationItem>
              <PaginationEllipsis v-else :key="`gap-${i}`" :index="i" />
            </template>
            <PaginationNext />
            <PaginationLast />
          </PaginationContent>
        </Pagination>
      </CardContent>
    </Card>

    <!-- Delete posts to the real handler, so the server stays the single source
         of truth — no client-side mutation. -->
    <Dialog :open="pending !== null" @update:open="(o) => !o && (pending = null)">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete “{{ pending?.name }}”?</DialogTitle>
          <DialogDescription>This cannot be undone.</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" @click="pending = null">Cancel</Button>
          <form :action="`${basePath}/${pending?.id}/delete`" method="post">
            <Button type="submit" variant="destructive">Delete</Button>
          </form>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </AdminLayout>
</template>
```

- [ ] **Step 2: Write the failing content test**

Append to `cmd/goappctl/internal/scaffold/admin_test.go`:

```go
func TestAdmin_IndexUsesTableAndOverlaysStartClosed(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "frontend/pages/admin/post/index.vue"))
	if err != nil {
		t.Fatalf("read index.vue: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"@tanstack/vue-table",
		"@/components/ui/table",
		"useVueTable",
		`const pending = ref<Post | null>(null)`, // overlay starts closed
		"No post yet.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("index.vue missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `:open="true"`) {
		t.Error("an overlay is server-rendered open; SSR does not emit teleported content")
	}
}
```

- [ ] **Step 3: Run it**

Run: `go test ./cmd/goappctl/internal/scaffold/ -run TestAdmin_IndexUses -v -count=1`

Expected: PASS once Step 1's template is in place. If `No post yet.` is missing, check that `[[.Table]]` renders as `post` for this resource name.

- [ ] **Step 4: Verify generated output compiles and type-checks**

```bash
go test ./cmd/goappctl/internal/scaffold/ -run OutputCompiles -count=1
go run ./cmd/goappctl gen admin uicheck --force
pnpm -C frontend type-check
rm -rf internal/controller/adminuicheck frontend/pages/admin/uicheck
```

Expected: both commands exit 0. `gen admin` prints a mount line; nothing needs pasting for a type-check.

- [ ] **Step 5: Commit**

```bash
git add cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl \
  cmd/goappctl/internal/scaffold/admin_test.go
git commit -m "feat(goappctl): generated admin list pages get a real data table

The <ul> becomes a TanStack-driven Table with sorting, a name filter, pagination
and a row-action menu; delete goes through a confirmation dialog that still posts
to the real handler. Sorting and paging are client-side over \`items\`, with a
comment pointing at the manualPagination switch.

The dialog starts closed by construction: SSR does not emit teleported content."
```

---

## Task 10: `gen admin` form template

**Files:**
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl`
- Test: `cmd/goappctl/internal/scaffold/resource_test.go` (the existing `TestGeneratedWriteHandlersValidate` asserts on this file)

**Interfaces:**
- Consumes: `@/components/ui/{card,label,input,button}`.
- Produces: unchanged props `item`, `basePath`, `errors?` — the validator spec's `errors?.<field>` rendering must survive.

- [ ] **Step 1: Replace the template**

```vue
<script setup lang="ts">
import AdminLayout from '@/components/AdminLayout.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface MenuItem { title: string; path: string; order?: number }

defineProps<{
  item: { id: number; name: string }
  basePath: string
  // Set by the handler only when a submit failed validation: one message per
  // bad field. `item` carries what was typed, so the inputs repopulate on their
  // own — there is no separate `old` prop.
  errors?: Record<string, string>
  // Injected by the admin auth middleware for the shell:
  adminMenu?: MenuItem[]
  adminUser?: unknown
  adminMount?: string
  loginPath?: string
  // One-shot messages staged by the handlers before their redirects:
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminLayout
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :flash="flash"
  >
    <Card class="max-w-lg">
      <CardHeader>
        <CardTitle>{{ item.id ? 'Edit' : 'New' }} [[.Type]]</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          :action="item.id ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <div class="space-y-1.5">
            <Label for="name">Name</Label>
            <Input
              id="name"
              name="name"
              :model-value="item.name"
              :aria-invalid="!!errors?.name"
              :class="errors?.name ? 'border-destructive' : ''"
            />
            <p v-if="errors?.name" class="text-sm text-destructive">{{ errors.name }}</p>
          </div>
          <div class="flex gap-2">
            <Button type="submit">Save</Button>
            <Button as="a" :href="basePath" variant="outline">Cancel</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminLayout>
</template>
```

- [ ] **Step 2: Update the existing validator assertions**

`TestGeneratedWriteHandlersValidate` in `cmd/goappctl/internal/scaffold/resource_test.go` asserts a shared list of form expectations including `:value="item.name"`. The shadcn `Input` binds `:model-value` instead, and only the admin template changed — so the shared list has to split. Add a `formWants` field to the case struct:

```go
	for _, c := range []struct {
		name      string
		gen       func(string, Options) error
		handler   string
		form      string
		formWants []string
	}{
		{
			"resource", Resource,
			"internal/controller/post/handler.go", "frontend/pages/post/form.vue",
			[]string{`errors?: Record<string, string>`, `errors?.name`, `:value="item.name"`},
		},
		{
			"admin", Admin,
			"internal/controller/adminpost/handler.go", "frontend/pages/admin/post/form.vue",
			[]string{`errors?: Record<string, string>`, `errors?.name`, `:model-value="item.name"`},
		},
	} {
```

and replace the hard-coded form loop with `for _, want := range c.formWants {`.

- [ ] **Step 3: Run the suite**

Run: `go test ./cmd/goappctl/... -count=1`

Expected: PASS, including `TestGeneratedWriteHandlersValidate` for both generators.

- [ ] **Step 4: Verify generated output type-checks**

```bash
go run ./cmd/goappctl gen admin uicheck --force
pnpm -C frontend type-check
rm -rf internal/controller/adminuicheck frontend/pages/admin/uicheck
```

Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl \
  cmd/goappctl/internal/scaffold/resource_test.go
git commit -m "feat(goappctl): generated admin forms use shadcn Card/Label/Input

The validator spec's contract is unchanged: \`item\` repopulates the inputs and
\`errors?.<field>\` renders beneath them, now with aria-invalid and the
destructive border. Only the admin template moves; gen resource keeps plain
Tailwind."
```

---

## Task 11: SSR regression test for a component-heavy admin page

The design was validated by a throwaway harness. Make it permanent, so a future change that breaks SSR under QuickJS fails the suite instead of production.

**Files:**
- Create: `server/ssr_admin_test.go`
- Modify: `cmd/goappctl/internal/components/components.go` (both `admin` and `ssr` own the new file)

**Interfaces:**
- Consumes: a built `frontend/dist/ssr-render-cjs.js`; `ssrBundleName` from `server/server.go`.
- Produces: nothing.

**Why the file needs two owners.** It cannot survive either component being trimmed: it reads `ssrBundleName`, which lives inside a `//goappctl:ssr` block; it imports `inertia/ssr` and `inertia/ssr/quickjs`, which leave `go.mod` with the `ssr` component; and it renders `admin/dashboard`, which leaves with `admin`. Listing the path under both `Owned` slices gives exactly the right semantics — `init` deletes the paths of every *off* component and tolerates ones already gone, so the file disappears if **either** is off.

- [ ] **Step 1: Write the test**

```go
//go:build !prod

package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/millken/inertia/ssr"
	"github.com/millken/inertia/ssr/quickjs"
)

// TestSSR_AdminDashboardRendersUnderQuickJS guards the SSR path for the shadcn
// component set. QuickJS has no document/window/Intl/ResizeObserver, and a
// component that touches them at module scope kills the whole bundle: the
// exports never get assigned and every page fails with a bare
// "TypeError: not a function". The RenderTemplate probe below distinguishes that
// from a component that merely throws while rendering.
func TestSSR_AdminDashboardRendersUnderQuickJS(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the QuickJS VM in -short mode")
	}
	const bundlePath = "../frontend/dist/" + ssrBundleName
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Skipf("no SSR bundle at %s; run `pnpm -C frontend build:ssr` first", bundlePath)
	}

	vm, err := quickjs.NewVM(ssr.WithDefaultCache(1), ssr.WithBundlerJS(string(bundle)))
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	defer vm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := vm.RenderTemplate(ctx, `<div>probe</div>`, map[string]any{}); err != nil {
		t.Fatalf("RenderTemplate probe failed, so the bundle's top-level init threw "+
			"and no page can render: %v", err)
	}

	html, err := vm.RenderComponent(ctx, "admin/dashboard", map[string]any{
		"adminUser":  "admin",
		"adminMount": "/admin",
		"loginPath":  "/admin/login",
		"adminMenu":  []map[string]any{{"title": "Posts", "path": "/admin/posts"}},
		"flash":      map[string]string{"success": "Saved"},
	})
	if err != nil {
		t.Fatalf("RenderComponent(admin/dashboard): %v", err)
	}
	for _, want := range []string{"Dashboard", "Posts", "Log out", "Saved"} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR output missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Build the SSR bundle and run the test**

Run: `pnpm -C frontend build:ssr && go test ./server/ -run TestSSR_Admin -v -count=1`

Expected: PASS.

- [ ] **Step 3: Confirm it skips rather than fails without a bundle**

```bash
mv frontend/dist/ssr-render-cjs.js /tmp/bundle.bak
go test ./server/ -run TestSSR_Admin -v -count=1
mv /tmp/bundle.bak frontend/dist/ssr-render-cjs.js
```

Expected: SKIP with the "run `pnpm -C frontend build:ssr` first" message. CI must not fail merely because the frontend was not built.

- [ ] **Step 4: Give the file two owners**

In `cmd/goappctl/internal/components/components.go`, add the same path to both slices — `admin`:

```go
			"frontend/src/components/ui",
			"frontend/src/lib",
			"server/ssr_admin_test.go",
```

and `ssr`:

```go
	{
		Name: "ssr",
		Owned: []string{
			"frontend/ssr",
			"frontend/ssr-esm-render.ts",
			"frontend/vite.config.ssr.ts",
			"server/ssr_admin_test.go",
		},
	},
```

- [ ] **Step 5: Prove a trimmed project still compiles**

The combination that would have broken is `admin` on, `ssr` off — the test file would survive while its imports left `go.mod`.

```bash
TMP=$(mktemp -d) && cp -r . "$TMP/app" && cd "$TMP/app" && rm -rf .git
go run ./cmd/goappctl init --module github.com/me/nossr --with db,session,admin --force
test ! -f server/ssr_admin_test.go && echo "SSR test removed with the ssr component"
go build ./... && go vet ./... && echo "trimmed project builds"
cd - && rm -rf "$TMP"
```

Expected: both messages print. `init` runs build/vet/test itself, so a leftover file would already have failed it — this makes the reason explicit.

- [ ] **Step 6: Commit**

```bash
git add server/ssr_admin_test.go cmd/goappctl/internal/components/components.go
git commit -m "test(server): guard admin SSR under QuickJS

The component evaluation found that a library touching document at module scope
kills the entire SSR bundle — exports never get assigned and every page fails
with a bare TypeError. A RenderTemplate probe distinguishes that from a
render-time throw, so a regression names its own cause.

Skips when the SSR bundle is absent, so CI does not require a frontend build.
Owned by both admin and ssr: it reads ssrBundleName, imports the quickjs runtime,
and renders an admin page, so trimming either component must take it along."
```

---

## Task 12: Document the boundary

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the component set to the project structure tree**

In the `## 项目结构` block, after the `frontend/src/components/` line inside the `goappctl:admin` markers, add:

```
├── frontend/src/components/ui/   #   复制进来的 shadcn-vue 组件（admin 专属）
├── frontend/src/lib/utils.ts     #   cn() / valueUpdater()
```

- [ ] **Step 2: Add a section after 「脚手架生成器」**

```markdown
## 后台 UI 组件

后台用 [shadcn-vue](https://www.shadcn-vue.com/)：组件**源码复制进仓库**（`frontend/src/components/ui/`），
不是 npm 依赖 —— 和 `gen resource` 产出一样，是「你拥有的普通文件」。数据表格能力来自
[@tanstack/vue-table](https://tanstack.com/table)（headless，只有逻辑）。

- **只服务后台。** 这些文件归 `admin` 组件所有，`goappctl init` 不选 admin 时连同
  `main.css` 里的主题块和 8 个 npm 依赖一起消失，公开页面体积回到原样。
- **`gen resource` 保持纯 Tailwind**，所以它在无 db / 无 session 的构建里照样可用。
- **不含表单校验组件。** 校验在服务端（[internal/validate](internal/validate/validate.go)），
  失败时重渲染并给出 `errors` prop —— 不需要客户端再来一套。

初始带 12 个组件：`alert` `badge` `button` `card` `dialog` `dropdown-menu` `input`
`label` `pagination` `select` `separator` `table`。加新组件：

```bash
cd frontend && pnpm dlx shadcn-vue@latest add combobox
```

**注意**：官方 CLI 拉取 registry 时可能失败（表现为 `Failed to fetch from registry`，
即使 curl 同一个 URL 正常）。手工替代路径是从
`https://shadcn-vue.com/r/styles/default/<name>.json` 取 JSON、按 `files[].path` 落盘
（`ui/**` → `frontend/src/components/ui/`），并把 `@/registry/default/ui` 改写成
`@/components/ui` —— 少了这步重写，构建会直接失败。

**不要在 SSR 阶段渲染打开的弹层。** `Dialog` / `DropdownMenu` / `Select` 的浮层走
Teleport，而 Vue 的 SSR renderer 把这类内容放进 `ctx.teleports`，
[frontend/ssr/render.ts](frontend/ssr/render.ts) 并未收集 —— 服务端不会输出它们，
客户端 hydration 时会凭空多出 DOM。弹层默认关闭即可。
```

- [ ] **Step 3: Verify the markers still balance**

Run: `go test ./cmd/goappctl/... -count=1`

Expected: PASS. The README's `goappctl:admin` blocks must stay balanced or `init` errors.

- [ ] **Step 4: Sanity-check a trimmed project end to end**

```bash
TMP=$(mktemp -d) && cp -r . "$TMP/app" && cd "$TMP/app" && rm -rf .git
go run ./cmd/goappctl init --module github.com/me/trimmed --with db --force

test ! -d frontend/src/components/ui \
  && echo "OK  ui/ removed" || echo "FAIL ui/ survived"
grep -q "reka-ui" frontend/package.json \
  && echo "FAIL reka-ui still in package.json" || echo "OK  deps pruned"
grep -q "goappctl:admin\|--color-background" frontend/src/styles/main.css \
  && echo "FAIL theme block survived" || echo "OK  theme stripped"
test ! -f frontend/src/lib/utils.ts \
  && echo "OK  lib/ removed" || echo "FAIL lib/ survived"

cd - && rm -rf "$TMP"
```

Expected: four `OK` lines and no `FAIL`. This is the whole trimming story in one run — a `FAIL` means Tasks 2/3/4 did not compose, and says which one.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: the admin UI component boundary

What ships, that it is admin-only and why, how to add components (including the
manual path for when the upstream CLI cannot reach its registry), and the
teleport/SSR constraint that will otherwise be rediscovered the hard way."
```

---

## Final verification

- [ ] **Step 1: Everything green**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && pnpm -C frontend type-check && pnpm -C frontend build`

Expected: all exit 0.

- [ ] **Step 2: Measure against the spec's numbers**

```bash
rm -rf frontend/dist/assets && pnpm -C frontend build:client >/dev/null 2>&1
fw=0; css=0; home=0
for f in frontend/dist/assets/*; do
  n=$(gzip -c "$f" | wc -c); b=$(basename "$f")
  case "$b" in
    Home-*) home=$n ;;
    login-*|dashboard-*) ;;
    *.css) css=$((css + n)) ;;
    *) fw=$((fw + n)) ;;
  esac
done
echo "public page: $(echo "scale=1;$((fw + css + home))/1024" | bc) kB gzip"
```

Expected: about **49.6 kB**, matching spec §10. A materially larger number means something public-facing started importing the component layer, which Task 4's boundary is supposed to prevent.

- [ ] **Step 3: Run the app**

Run: `make dev`, then open `http://localhost:8080/admin` and sign in with `admin` / `admin`.

Expected: the shadcn shell renders, the dashboard card shows, and the login page styles correctly. `make dev` seeds the admin user itself.
