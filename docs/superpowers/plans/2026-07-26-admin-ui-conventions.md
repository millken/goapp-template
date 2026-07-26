# Admin UI Conventions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Codify the admin UI as composite components (two-column menu shell with breadcrumb, DataTable, PageHeader, FormField, ConfirmDialog, ThemeToggle) so every later admin page is assembled, not designed.

**Architecture:** Six Vue composites in a new admin-owned directory replace the inline plumbing in the generated templates and the single-column `AdminLayout`. The backend gains menu sections, a username in the `adminUser` prop (from the query `findGroup` already runs), and a `currentPath` prop; both HTML shells gain a no-FOUC dark-mode boot script behind `goappctl:admin` markers.

**Tech Stack:** Vue 3 + shadcn-vue copies + @tanstack/vue-table + lucide-vue-next (all existing); Go 1.26; vitest + happy-dom (existing).

**Spec:** `docs/superpowers/specs/2026-07-26-admin-ui-conventions-design.md` — read it before any task.

## Global Constraints

- Visual tokens in `frontend/src/styles/main.css` are **untouched** — neutral gray stays.
- **No new dependencies**, runtime or dev. The DataTable test mounts via `createApp` directly, so `@vue/test-utils` is not added.
- Scaffold templates use Go template `[[ ]]` delimiters (`render.go` sets `.Delims("[[", "]]")`); generated output must contain no `[[` or `]]`.
- One group lookup per request — nothing in these tasks adds a query to `resolve`.
- Overlays start closed (SSR emits no teleported content); no `<button>` may nest inside a `<button>` — both pinned by `server/ssr_fixture_test.go`.
- Signature changes are clean breaks — no back-compat shims; this is repo-internal API.
- Verification for every Go task: `go build ./...`, `go vet ./...`, `gofmt -l ./` silent, `go test ./... -count=1`. For every frontend task: `pnpm -C frontend run type-check`, `pnpm -C frontend run test`, `pnpm -C frontend run build`.
- Empty menu / empty sections still reach the frontend as `[]`, never `null`.

## File Structure

```
cmd/goappctl/internal/markers/markers.go        Task 1: .html form
internal/controller/admin/{menu,permission}.go  Task 2: sections
cmd/.../templates/admin/handler.go.tmpl         Task 2: 3-arg Menu call
internal/controller/admin/{group,auth}.go       Task 3: username + currentPath
frontend/index.html, server/server.go           Task 4: dark boot script
frontend/src/components/admin/                  Tasks 5–7: the composites
  PageHeader.vue FormField.vue ConfirmDialog.vue ThemeToggle.vue   (5)
  DataTable.vue DataTable.test.ts               (6, + vitest.config.ts plugins)
  AdminShell.vue                                (7, deletes ../AdminLayout.vue)
cmd/.../templates/admin/{index,form}.vue.tmpl   Task 8: rewrite + fixture regen
README.md                                       Task 9: 设计约定
```

---

### Task 1: `.html` marker form

`goappctl init` strips `goappctl:<name>` blocks, choosing comment syntax by file extension. `frontend/index.html` needs a marker in Task 4, and today `initcmd` **errors** on a marker in an unsupported file type. HTML comments are exactly the `.md` form.

**Files:**
- Modify: `cmd/goappctl/internal/markers/markers.go:33-40` (the `forms` map)
- Test: `cmd/goappctl/internal/markers/markers_test.go`

**Interfaces:**
- Produces: `markers.Supported("frontend/index.html") == true`; `.html` blocks strip like `.md` blocks.

- [ ] **Step 1: Write the failing test** (append to `markers_test.go`; the `opts(...)` helper is at the top of that file)

```go
// index.html carries the admin component's dark-mode boot script, so .html
// needs a comment form — without one, init errors on any marker in the file.
func TestStrip_HTMLForm(t *testing.T) {
	src := "<head>\n<!--goappctl:admin-->\n<script>dark()</script>\n<!--goappctl:end-->\n<title>x</title>\n</head>\n"

	got, n, err := Strip("index.html", []byte(src), opts("admin"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	if want := "<head>\n<title>x</title>\n</head>\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Keeping the block must unwrap the markers only.
	kept, _, err := Strip("index.html", []byte(src), opts())
	if err != nil {
		t.Fatalf("Strip keep: %v", err)
	}
	if want := "<head>\n<script>dark()</script>\n<title>x</title>\n</head>\n"; string(kept) != want {
		t.Errorf("kept %q, want %q", kept, want)
	}
	if !Supported("frontend/index.html") {
		t.Error("Supported(.html) = false")
	}
}
```

- [ ] **Step 2: Run it — must fail**

Run: `go test ./cmd/goappctl/internal/markers/ -run TestStrip_HTMLForm -v`
Expected: FAIL — `Strip` returns the source unchanged (`n = 0`) because `.html` has no form, and `Supported` is false.

- [ ] **Step 3: Add the form** — in the `forms` map, after the `.md` line:

```go
	".html": {open: "<!--goappctl:", end: "<!--goappctl:end-->", close: "-->"},
```

- [ ] **Step 4: Run the package tests**

Run: `go test ./cmd/goappctl/internal/markers/ -count=1 -v`
Expected: all PASS.

- [ ] **Step 5: Full verification and commit**

```bash
go build ./... && go vet ./... && gofmt -l ./ && go test ./... -count=1
git add cmd/goappctl/internal/markers/
git commit -m "feat(goappctl): markers learns the .html comment form"
```

---

### Task 2: Menu sections

The shell groups the menu into sections (icon rail → section panel). `MenuItem` gains `Section`; `Registrar.Menu` takes it as a new first argument (clean break); sections display in first-registration order, items within a section by the existing Order-then-Title rule. Permission filtering is untouched. An empty section defaults to `"Content"` — normalized at registration so the stored menu is already canonical.

**Files:**
- Modify: `internal/controller/admin/menu.go`
- Modify: `internal/controller/admin/permission.go` (the `Menu` method, currently `Menu(title, path string)`)
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/handler.go.tmpl` (the `r.Menu("[[.Type]]", ct.base)` line — the generated-output compile test breaks otherwise)
- Test: `internal/controller/admin/menu_test.go`, `internal/controller/admin/permission_test.go`, `cmd/goappctl/internal/scaffold/admin_test.go`

**Interfaces:**
- Consumes: `menuEntry`, `menuItems(g *group) []MenuItem`, `permSet.Allows` — all existing.
- Produces: `MenuItem{Title, Path, Order int, Section string}`; `(*Registrar).Menu(section, title, path string)`; `AddMenuItem(item MenuItem)` unchanged signature (callers may set `Section`; empty → `"Content"`).

- [ ] **Step 1: Write the failing tests** (append to `menu_test.go`)

```go
// Sections display in first-registration order — not alphabetically — so the
// wiring order in serve.go is the one knob controlling the rail. Items inside
// a section keep the Order-then-Title rule.
func TestMenuItems_SectionOrderFollowsRegistration(t *testing.T) {
	a := New(nil, nil)
	a.AddMenuItem(MenuItem{Title: "Settings", Path: "/admin/settings", Section: "System"})
	a.AddMenuItem(MenuItem{Title: "Groups", Path: "/admin/group", Section: "Access"})
	a.AddMenuItem(MenuItem{Title: "Users", Path: "/admin/user", Section: "Access"})
	a.AddMenuItem(MenuItem{Title: "Posts", Path: "/admin/post"}) // empty → "Content"

	got := a.menuItems(&group{Superuser: true})
	titles := make([]string, len(got))
	for i, m := range got {
		titles[i] = m.Section + ":" + m.Title
	}
	want := "System:Settings,Access:Groups,Access:Users,Content:Posts"
	if s := strings.Join(titles, ","); s != want {
		t.Errorf("menu = %q, want %q", s, want)
	}
}
```

And in `permission_test.go`, find `TestRegistrar_RegistersRoutesAndRecordsKeys` and change its Menu call plus assertion:

```go
	r.Menu("Content", "Post", "/admin/post")
```

and where it asserts `a.menu[0].resource != "post"`, extend:

```go
	if len(a.menu) != 1 || a.menu[0].resource != "post" || a.menu[0].item.Section != "Content" {
		t.Errorf("Menu should record the resource and section, got %+v", a.menu)
	}
```

- [ ] **Step 2: Run — must fail to compile** (Section field and 3-arg Menu don't exist)

Run: `go test ./internal/controller/admin/ -run 'TestMenuItems_SectionOrder|TestRegistrar_RegistersRoutes' 2>&1 | head -5`
Expected: compile errors `unknown field Section` / `too many arguments`.

- [ ] **Step 3: Implement.** In `menu.go`:

```go
// MenuItem — add after Order:
	// Section groups items in the shell's icon rail; empty means "Content".
	Section string `json:"section"`
```

Normalize in both registration funcs (`AddMenuItem` and `addResourceMenuItem` bodies):

```go
func (a *Admin) AddMenuItem(item MenuItem) {
	if item.Section == "" {
		item.Section = "Content"
	}
	a.menu = append(a.menu, menuEntry{item: item})
}

func (a *Admin) addResourceMenuItem(item MenuItem, resource string) {
	if item.Section == "" {
		item.Section = "Content"
	}
	a.menu = append(a.menu, menuEntry{item: item, resource: resource})
}
```

Replace `menuItems`' sort with a section-aware one (section index computed from the **unfiltered** menu, so a section keeps its place even when this caller can't see its first item):

```go
	secIdx := make(map[string]int)
	for _, e := range a.menu {
		if _, ok := secIdx[e.item.Section]; !ok {
			secIdx[e.item.Section] = len(secIdx)
		}
	}
	slices.SortStableFunc(out, func(x, y MenuItem) int {
		return cmp.Or(
			cmp.Compare(secIdx[x.Section], secIdx[y.Section]),
			cmp.Compare(x.Order, y.Order),
			cmp.Compare(x.Title, y.Title),
		)
	})
```

In `permission.go`, replace the `Menu` method:

```go
// Menu adds the resource's sidebar entry under section (the icon-rail group),
// shown only to callers holding the resource's access key.
func (r *Registrar) Menu(section, title, path string) {
	r.admin.addResourceMenuItem(MenuItem{Title: title, Path: path, Section: section}, r.resource)
}
```

In `handler.go.tmpl`, change the Menu line to:

```go
	r.Menu("Content", "[[.Type]]", ct.base)
```

In `scaffold/admin_test.go` `TestAdmin_RoutesGoThroughTheRegistrar`, update the want-string:

```go
		`r.Menu("Content", "Post", ct.base)`,
```

- [ ] **Step 4: Run the affected packages, then everything**

Run: `go test ./internal/controller/admin/ ./cmd/goappctl/... -count=1`
Expected: PASS — including the request-level `TestAdminMenuPropIsFilteredOnBothMiddlewares` (its entries all default to `Content`, so its `"Docs,Post"` expectation is unchanged) and the generated-output compile/staleness tests.
Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/admin/ cmd/goappctl/
git commit -m "feat(admin): menu sections for the two-column shell"
```

---

### Task 3: `adminUser` carries the username; `currentPath` prop

The topbar shows a username, but the prop carries the raw session id. `findGroup`'s query already joins `users` — add `u.username` to its SELECT and return it; still one query per request. `resolve` also injects `currentPath` so the shell computes breadcrumb/active state without touching `location` (QuickJS SSR has no `location`).

**Files:**
- Modify: `internal/controller/admin/group.go:27-52` (`findGroup`)
- Modify: `internal/controller/admin/auth.go:41-59` (`resolve`)
- Test: `internal/controller/admin/group_test.go`, `internal/controller/admin/auth_test.go`

**Interfaces:**
- Consumes: `resolve`'s existing flow; `loginStack` seeds user `alice`.
- Produces: `findGroup(ctx, d, usersTable, userID) (*group, string, error)` — group, username; `resolve` unchanged `(*group, bool)`, but now sets `adminUser` to `map[string]any{"id": int64, "username": string}` and `currentPath` to `c.Request.URL.Path`.

- [ ] **Step 1: Write the failing test** (append to `auth_test.go`)

```go
// The shell's topbar shows who is signed in and where they are; both come from
// resolve, in the same single query it already ran.
func TestResolve_InjectsUsernameAndCurrentPath(t *testing.T) {
	eng, adm := loginStack(t)

	var gotUser any
	var gotPath any
	eng.GET("/admin/probe", adm.AuthMiddleware(), func(ic *inertia.Context) {
		gotUser, _ = ic.Get("adminUser")
		gotPath, _ = ic.Get("currentPath")
	})
	cookie := loginAndGetCookie(t, eng)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/admin/probe", nil)
	r.AddCookie(cookie)
	eng.ServeHTTP(w, r)

	u, ok := gotUser.(map[string]any)
	if !ok {
		t.Fatalf("adminUser = %#v, want a map", gotUser)
	}
	if u["username"] != "alice" {
		t.Errorf("adminUser.username = %v, want alice", u["username"])
	}
	if u["id"] == nil {
		t.Error("adminUser.id missing")
	}
	if gotPath != "/admin/probe" {
		t.Errorf("currentPath = %v, want /admin/probe", gotPath)
	}
}
```

- [ ] **Step 2: Run — must fail**

Run: `go test ./internal/controller/admin/ -run TestResolve_InjectsUsernameAndCurrentPath -v`
Expected: FAIL — `adminUser` is the raw int64 session value, not a map.

- [ ] **Step 3: Implement.** `findGroup` becomes:

```go
// findGroup loads the group and username of the user with userID. usersTable is
// interpolated (it is configurable) and has already been validated by
// Admin.Validate against ^[A-Za-z_]\w*$; the id itself is parameterised. The
// username rides along because the topbar shows it, and adding a second query
// for one column would break the one-lookup-per-request rule.
func findGroup(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*group, string, error) {
	q := fmt.Sprintf(`SELECT u.username, g.superuser, g.permissions
		FROM %s u JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, usersTable)

	var username string
	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&username, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, "", errNoGroup
		}
		return nil, "", fmt.Errorf("admin: find group: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, "", fmt.Errorf("admin: group %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return &group{Superuser: superuser != 0, Permissions: set}, username, nil
}
```

In `resolve`, update the call and the prop block (the username is consumed here; `resolve`'s own return is unchanged):

```go
	g, username, err := findGroup(c.Request.Context(), a.DB, a.usersTable(), id)
```

and replace `c.Set("adminUser", v)` with:

```go
	c.Set("adminUser", map[string]any{"id": id, "username": username})
	c.Set("currentPath", c.Request.URL.Path)
```

Update `group_test.go`'s three direct `findGroup` callers to the three-value form (`g, _, err := findGroup(...)` — and in the happy-path test, assert the username too: `g, name, err := ...; if name != "..." { t.Errorf(...) }` using whatever username its fixture seeds).

- [ ] **Step 4: Run everything**

Run: `go test ./... -count=1`
Expected: PASS — including `TestResolve_WorksWithTheDatabaseSessionStore` (the map is built server-side per request, so the JSON store round-trip never sees it).

- [ ] **Step 5: Commit**

```bash
git add internal/controller/admin/
git commit -m "feat(admin): adminUser carries the username; resolve injects currentPath"
```

---

### Task 4: Dark-mode boot script in both HTML shells

No-FOUC dark mode needs one line of JS to run before first paint. Two shells exist: `frontend/index.html` (Vite dev) and `server/server.go`'s `rootHTML` (prod string template). The `.html` file uses the Task 1 marker form. `rootHTML`'s HTML lives **inside a Go string literal**, so a marker inside the string would be served to browsers as text — instead the script is a separate Go assignment wrapped in ordinary `.go` markers outside the literal, stripped to the empty string when admin is off.

**Files:**
- Modify: `frontend/index.html`
- Modify: `server/server.go:111-139` (`rootHTML`)
- Test: `server/roothtml_test.go` (create)

**Interfaces:**
- Consumes: Task 1's `.html` marker form.
- Produces: both shells' `<head>` contains the boot script; `goappctl init` without admin strips it from both.

The boot script (identical in both places):

```html
<script>try{if(localStorage.theme==='dark'||(!('theme' in localStorage)&&matchMedia('(prefers-color-scheme: dark)').matches))document.documentElement.classList.add('dark')}catch(e){}</script>
```

- [ ] **Step 1: Write the failing test** (create `server/roothtml_test.go`)

```go
//go:build !prod

package server

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The dark class must be set before first paint or the page flashes light. The
// script lives in rootHTML as a marker-wrapped Go assignment — a marker inside
// the HTML string literal would be served to browsers as text.
func TestRootHTML_CarriesTheDarkBootScript(t *testing.T) {
	html := rootHTML(fstest.MapFS{})
	for _, want := range []string{"localStorage.theme", "prefers-color-scheme", "classList.add('dark')"} {
		if !strings.Contains(html, want) {
			t.Errorf("rootHTML missing %q", want)
		}
	}
	if strings.Contains(html, "goappctl:") {
		t.Error("a goappctl marker leaked into the served HTML")
	}
}
```

- [ ] **Step 2: Run — must fail**

Run: `go test ./server/ -run TestRootHTML_CarriesTheDarkBootScript -v`
Expected: FAIL — no boot script yet.

- [ ] **Step 3: Implement.** In `rootHTML`, before the `return fmt.Sprintf(...)`:

```go
	// Set the dark class before first paint, or the page flashes light. The
	// assignment (not the string) carries the markers: this HTML lives in a Go
	// string literal, where a marker line would be served to browsers as text.
	darkBoot := ""
	//goappctl:admin
	darkBoot = `<script>try{if(localStorage.theme==='dark'||(!('theme' in localStorage)&&matchMedia('(prefers-color-scheme: dark)').matches))document.documentElement.classList.add('dark')}catch(e){}</script>`
	//goappctl:end
```

Add `%s` for it in the head — after `<title>App</title>` — and `darkBoot` to the `fmt.Sprintf` arguments (before `cssLink`):

```go
  <title>App</title>
  %s
  %s
```

In `frontend/index.html`, inside `<head>` after `<title>App</title>`:

```html
    <!--goappctl:admin-->
    <script>try{if(localStorage.theme==='dark'||(!('theme' in localStorage)&&matchMedia('(prefers-color-scheme: dark)').matches))document.documentElement.classList.add('dark')}catch(e){}</script>
    <!--goappctl:end-->
```

- [ ] **Step 4: Verify strip behaviour and run everything**

Run: `go test ./server/ ./cmd/goappctl/... -count=1` — PASS expected (initcmd walks real repo files in its combo tests; a failure here means the `.html` form or the Go marker block is malformed).
Run: `go test ./... -count=1` — PASS.
Then confirm the strip end-to-end (initcmd's own tests already run `init` against the repo; this eyeballs the two files):

```bash
go run ./cmd/goappctl init --dry-run 2>&1 | grep -Ei "index.html|server.go" || true
```

Expected: both files listed as modified when admin is off in the chosen combo (flag syntax: whatever `init --help` shows; the point is the two files appear as strip targets).

- [ ] **Step 5: Commit**

```bash
git add frontend/index.html server/
git commit -m "feat(admin): no-FOUC dark-mode boot script in both HTML shells"
```

---

### Task 5: Small composites — PageHeader, FormField, ConfirmDialog, ThemeToggle

Four leaf components in the new admin-owned directory. No vitest here (spec mandates it only for DataTable); they are exercised through the SSR fixture in Task 8 and type-checked now.

**Files:**
- Create: `frontend/src/components/admin/PageHeader.vue`
- Create: `frontend/src/components/admin/FormField.vue`
- Create: `frontend/src/components/admin/ConfirmDialog.vue`
- Create: `frontend/src/components/admin/ThemeToggle.vue`

**Interfaces:**
- Consumes: `@/components/ui/{label,button,dialog}`, `lucide-vue-next` (existing).
- Produces: the four components exactly as below — Tasks 7–8 import them by these paths and props.

- [ ] **Step 1: `PageHeader.vue`**

```vue
<script setup lang="ts">
// Every admin page opens with one: title + optional description left, actions
// right. The breadcrumb answers "where am I"; this answers "what is this page".
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="mb-6 flex items-start justify-between gap-4">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="description" class="mt-1 text-sm text-muted-foreground">{{ description }}</p>
    </div>
    <div class="flex shrink-0 gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
```

- [ ] **Step 2: `FormField.vue`**

```vue
<script setup lang="ts">
// Label + control + server-side error, the repeating unit of every admin form.
// Errors come only from the server's `errors` prop — there is no client-side
// validation. The wrapper styles the slotted control when invalid, so pages
// don't repeat the border class.
import { Label } from '@/components/ui/label'

defineProps<{ name: string; label: string; error?: string }>()
</script>

<template>
  <div
    class="space-y-1.5"
    :class="error ? '[&_input]:border-destructive [&_select]:border-destructive [&_textarea]:border-destructive' : ''"
  >
    <Label :for="name">{{ label }}</Label>
    <slot />
    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
  </div>
</template>
```

- [ ] **Step 3: `ConfirmDialog.vue`**

```vue
<script setup lang="ts">
// The only dialog in the conventions: destructive confirmation. The confirm
// button submits a plain form POST to `action`, so the server stays the single
// source of truth — no client-side mutation. Dialogs start closed; SSR emits no
// teleported content, and server/ssr_fixture_test.go asserts none renders.
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

defineProps<{
  open: boolean
  title: string
  description?: string
  action: string
  confirmLabel?: string
}>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
</script>

<template>
  <Dialog :open="open" @update:open="(o) => emit('update:open', o)">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ description ?? 'This cannot be undone.' }}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="emit('update:open', false)">Cancel</Button>
        <form :action="action" method="post">
          <Button type="submit" variant="destructive">{{ confirmLabel ?? 'Delete' }}</Button>
        </form>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
```

- [ ] **Step 4: `ThemeToggle.vue`**

```vue
<script setup lang="ts">
// Toggles the dark class and persists the choice. Initial state is read in
// onMounted from the class the boot script already set — nothing here touches
// document at setup time, so the component is inert under QuickJS SSR.
import { onMounted, ref } from 'vue'
import { Moon, Sun } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

const dark = ref(false)
onMounted(() => {
  dark.value = document.documentElement.classList.contains('dark')
})
function toggle() {
  dark.value = !dark.value
  document.documentElement.classList.toggle('dark', dark.value)
  localStorage.theme = dark.value ? 'dark' : 'light'
}
</script>

<template>
  <Button variant="ghost" size="icon-sm" aria-label="Toggle theme" @click="toggle">
    <Sun v-if="!dark" />
    <Moon v-else />
  </Button>
</template>
```

- [ ] **Step 5: Verify and commit** (nothing imports them yet — this checks they compile)

```bash
pnpm -C frontend run type-check && pnpm -C frontend run build
git add frontend/src/components/admin/
git commit -m "feat(admin-ui): PageHeader, FormField, ConfirmDialog, ThemeToggle composites"
```

---

### Task 6: DataTable + its vitest

The big one: the ~150-line TanStack pipeline from `index.vue.tmpl` (search / sort / client pagination / empty state / row-actions dropdown / the numbered-page pager) becomes one component. Column defs are a simple spec, not raw `ColumnDef`. The pager's shape is load-bearing: **First/Previous/Next/Last are siblings of the page items, never wrapped in `PaginationItem`** — that nests a button in a button, the exact bug `server/ssr_fixture_test.go` exists to catch.

Mounting an SFC under vitest needs the Vue plugin in the **vitest** config (the pjax tests are plain `.ts`, so it was never needed before). `@vitejs/plugin-vue` is already a devDependency; no new packages.

**Files:**
- Create: `frontend/src/components/admin/DataTable.vue`
- Create: `frontend/src/components/admin/DataTable.test.ts` (collected by the existing `src/**/*.test.ts` glob)
- Modify: `frontend/vitest.config.ts` (add the plugin)

**Interfaces:**
- Consumes: `@tanstack/vue-table`, `@/components/ui/{button,dropdown-menu,input,pagination,table}`, `valueUpdater` from `@/lib/utils`.
- Produces: `DataTable` with props `{ columns: {key,label,sortable?}[], data: Record<string,unknown>[], searchKey?: string, pageSize?: number /* default 20 */ }`, slots `#cell-<key>="{ row }"`, `#row-actions="{ row }"`, `#empty`.

- [ ] **Step 1: Add the Vue plugin to `frontend/vitest.config.ts`**

```ts
import vue from '@vitejs/plugin-vue'
```

and inside `defineConfig({ ... })`, alongside `test`/`resolve`:

```ts
  // SFC support: DataTable.test.ts mounts a .vue component. The pjax tests are
  // plain TS and never needed this.
  plugins: [vue()],
```

- [ ] **Step 2: Write the failing test** (create `DataTable.test.ts`)

```ts
// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import DataTable from './DataTable.vue'

// Mounted via createApp directly: the repo deliberately has no @vue/test-utils,
// and a table renders enough real DOM to assert on.
function mount(props: Record<string, unknown>, slots: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(DataTable, props, slots) }).mount(el)
  return el
}

const rows = (n: number) =>
  Array.from({ length: n }, (_, i) => ({ id: i + 1, name: `row ${String(i + 1).padStart(2, '0')}` }))

const columns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: 'Name', sortable: true },
]

const bodyTexts = (el: HTMLElement) =>
  [...el.querySelectorAll('tbody td')].map((td) => td.textContent?.trim())

describe('DataTable', () => {
  it('renders a page of rows and slices at pageSize', () => {
    const el = mount({ columns, data: rows(25), pageSize: 20 })
    expect(el.querySelectorAll('tbody tr').length).toBe(20)
    expect(el.textContent).toContain('row 01')
    expect(el.textContent).not.toContain('row 21') // page two
  })

  it('filters on searchKey', async () => {
    const el = mount({ columns, data: rows(25), searchKey: 'name' })
    const input = el.querySelector('input')!
    input.value = 'row 07'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
    expect(el.querySelectorAll('tbody tr').length).toBe(1)
    expect(el.textContent).toContain('row 07')
  })

  it('sorts when a sortable header is clicked', async () => {
    const el = mount({ columns, data: rows(5) })
    const sortBtn = [...el.querySelectorAll('thead button')].find((b) =>
      b.textContent?.includes('Name'),
    ) as HTMLButtonElement
    sortBtn.click() // asc
    await nextTick()
    sortBtn.click() // desc
    await nextTick()
    expect(bodyTexts(el)[1]).toBe('row 05')
  })

  it('renders a #cell-<key> slot instead of the raw value', () => {
    const el = mount(
      { columns, data: rows(2) },
      { 'cell-name': ({ row }: { row: Record<string, unknown> }) => h('em', String(row.name)) },
    )
    expect(el.querySelectorAll('tbody em').length).toBe(2)
  })

  it('shows the empty state when no rows match', () => {
    const el = mount({ columns, data: [] })
    expect(el.textContent).toContain('No results.')
  })
})
```

- [ ] **Step 3: Run — must fail**

Run: `pnpm -C frontend run test`
Expected: FAIL — `DataTable.vue` does not exist.

- [ ] **Step 4: Implement `DataTable.vue`**

```vue
<script setup lang="ts">
// The list-page table: search, sort, client-side pagination, empty state and a
// row-actions dropdown, in one component so generated pages carry column defs
// instead of plumbing. Column defs are the simple spec below rather than raw
// TanStack ColumnDef — a page needing more edits this file (it is owned code,
// same as the shadcn components). Swap in server-side paging by adding query
// params to the handler and setting manualPagination here.
import { computed, h, ref } from 'vue'
import {
  FlexRender, getCoreRowModel, getFilteredRowModel, getPaginationRowModel,
  getSortedRowModel, useVueTable,
  type ColumnDef, type ColumnFiltersState, type SortingState,
} from '@tanstack/vue-table'
import { ArrowUpDown, MoreHorizontal } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuTrigger,
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

export interface DataTableColumn {
  key: string
  label: string
  sortable?: boolean
}
type Row = Record<string, unknown>

const props = withDefaults(defineProps<{
  columns: DataTableColumn[]
  data: Row[]
  searchKey?: string
  pageSize?: number
}>(), { pageSize: 20 })

const slots = defineSlots<
  { [K: `cell-${string}`]: (p: { row: Row }) => unknown }
  & { 'row-actions'?: (p: { row: Row }) => unknown; empty?: () => unknown }
>()

const sorting = ref<SortingState>([])
const columnFilters = ref<ColumnFiltersState>([])

const columnDefs = computed<ColumnDef<Row>[]>(() =>
  props.columns.map((c) => ({
    accessorKey: c.key,
    header: c.sortable
      ? ({ column }) =>
          h(
            Button,
            {
              variant: 'ghost',
              class: '-ml-4',
              onClick: () => column.toggleSorting(column.getIsSorted() === 'asc'),
            },
            () => [c.label, h(ArrowUpDown, { class: 'ml-2 size-4' })],
          )
      : c.label,
  })),
)

const table = useVueTable({
  get data() { return props.data },
  get columns() { return columnDefs.value },
  getCoreRowModel: getCoreRowModel(),
  getSortedRowModel: getSortedRowModel(),
  getFilteredRowModel: getFilteredRowModel(),
  getPaginationRowModel: getPaginationRowModel(),
  onSortingChange: (u) => valueUpdater(u, sorting),
  onColumnFiltersChange: (u) => valueUpdater(u, columnFilters),
  initialState: { pagination: { pageSize: props.pageSize } },
  state: {
    get sorting() { return sorting.value },
    get columnFilters() { return columnFilters.value },
  },
})

const setFilter = (v: string | number) => {
  if (props.searchKey) table.getColumn(props.searchKey)?.setFilterValue(String(v))
}
const colspan = computed(() => props.columns.length + (slots['row-actions'] ? 1 : 0))
</script>

<template>
  <div class="space-y-4">
    <Input
      v-if="searchKey"
      :placeholder="`Filter by ${searchKey}…`"
      class="max-w-xs"
      @update:model-value="setFilter"
    />

    <Table>
      <TableHeader>
        <TableRow v-for="hg in table.getHeaderGroups()" :key="hg.id">
          <TableHead v-for="header in hg.headers" :key="header.id">
            <FlexRender :render="header.column.columnDef.header" :props="header.getContext()" />
          </TableHead>
          <TableHead v-if="slots['row-actions']" class="w-12" />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="row in table.getRowModel().rows" :key="row.id">
          <TableCell v-for="cell in row.getVisibleCells()" :key="cell.id">
            <slot :name="`cell-${cell.column.id}`" :row="row.original">
              <FlexRender :render="cell.column.columnDef.cell" :props="cell.getContext()" />
            </slot>
          </TableCell>
          <TableCell v-if="slots['row-actions']">
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon-sm"><MoreHorizontal /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <slot name="row-actions" :row="row.original" />
              </DropdownMenuContent>
            </DropdownMenu>
          </TableCell>
        </TableRow>
        <TableEmpty v-if="!table.getRowModel().rows.length" :colspan="colspan">
          <slot name="empty">No results.</slot>
        </TableEmpty>
      </TableBody>
    </Table>

    <!-- Page state lives in the table: reka-ui emits update:page and we forward
         it, so there is one source of truth. First/Previous/Next/Last are
         siblings of the page items, never parents — wrapping one in a
         PaginationItem nests a <button> inside a <button>, which browsers
         reparse and hydration then disagrees with. PaginationItem is itself a
         button (it applies buttonVariants), so the page number goes in its slot
         rather than in a nested Button. -->
    <Pagination
      v-if="table.getPageCount() > 1"
      :items-per-page="pageSize"
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
  </div>
</template>
```

- [ ] **Step 5: Run the tests**

Run: `pnpm -C frontend run test`
Expected: 5 new tests PASS (plus the 4 existing pjax suites).

- [ ] **Step 6: Verify and commit**

```bash
pnpm -C frontend run type-check && pnpm -C frontend run build
git add frontend/src/components/admin/ frontend/vitest.config.ts
git commit -m "feat(admin-ui): DataTable composite wraps the TanStack pipeline"
```

---

### Task 7: AdminShell — two-column menu, breadcrumb, topbar

Replaces `AdminLayout.vue` (deleted, no alias). Icon rail (sections) + section panel (items) + topbar (breadcrumb left; ThemeToggle and user dropdown right) + flash + content slot. The shell contributes a hardcoded Home section pointing at the mount — deliberately outside `menuItems`' permission filtering, because the dashboard is an exempt route. Path awareness comes from the `currentPath` prop (Task 3), falling back to `location.pathname` client-side; QuickJS SSR has neither `location` nor `matchMedia`, so nothing may touch them at setup time.

**Files:**
- Create: `frontend/src/components/admin/AdminShell.vue`
- Delete: `frontend/src/components/AdminLayout.vue`
- Modify: `frontend/pages/admin/dashboard.vue`
- Modify: `cmd/goappctl/internal/components/components.go:48-56` (the admin `Owned` list)
- Modify: `server/ssr_admin_test.go:46-60` (props + assertions)

**Interfaces:**
- Consumes: `ThemeToggle` (Task 5), `MenuItem` JSON now carrying `section` (Task 2), `adminUser` map + `currentPath` (Task 3).
- Produces: `AdminShell` with props `{ menu?: MenuItem[]; user?: { id?: number; username?: string }; mount?: string; loginPath?: string; currentPath?: string; flash?: Record<string,string>; crumb?: string }`. Tasks 8's templates wrap pages in it.

- [ ] **Step 1: Write `AdminShell.vue`**

```vue
<script setup lang="ts">
// The admin frame: icon rail (menu sections) + section panel (items of the
// active section) + topbar (breadcrumb | theme toggle + user menu) + flash +
// content. Two columns instead of a multi-level tree: each section gets a full
// column of items.
//
// Navigation stays plain <a href> — the PJAX layer intercepts through
// document-level delegation. Path awareness comes from the currentPath prop
// (injected by resolve, present under SSR too); location is only a client-side
// fallback, never touched at setup time — QuickJS has no location.
import { computed } from 'vue'
import { ChevronDown, FileText, Gauge, LogOut, Settings, Users } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import ThemeToggle from '@/components/admin/ThemeToggle.vue'

interface MenuItem {
  title: string
  path: string
  order?: number
  section?: string
}

const props = defineProps<{
  menu?: MenuItem[]
  user?: { id?: number; username?: string }
  mount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  crumb?: string
}>()

const base = computed(() => props.mount || '/admin')
const path = computed(() =>
  props.currentPath ?? (typeof location === 'undefined' ? base.value : location.pathname),
)

// Home is hardcoded, not a registered menu item: the dashboard is an exempt
// route, so every signed-in user sees it regardless of permission filtering.
const sections = computed(() => {
  const home = { title: 'Home', items: [{ title: 'Overview', path: base.value }] }
  const out: { title: string; items: MenuItem[] }[] = [home]
  const idx = new Map<string, number>()
  for (const item of props.menu ?? []) {
    const name = item.section || 'Content'
    let i = idx.get(name)
    if (i === undefined) {
      i = out.length
      idx.set(name, i)
      out.push({ title: name, items: [] })
    }
    out[i].items.push(item)
  }
  return out
})

// An item is active when the path equals it or extends it with a slash, so
// /admin/user/3/edit lights up Users. Longest match wins; no match → Home.
const active = computed(() => {
  let best: { section: number; item: MenuItem } | null = null
  sections.value.forEach((s, si) => {
    for (const it of s.items) {
      const hit = path.value === it.path || path.value.startsWith(it.path + '/')
      if (hit && (!best || it.path.length > best.item.path.length)) {
        best = { section: si, item: it }
      }
    }
  })
  return best ?? { section: 0, item: sections.value[0].items[0] }
})

const crumbs = computed(() => {
  const s = sections.value[active.value.section]
  const list: { title: string; path?: string }[] = [
    { title: s.title, path: s.items[0].path },
    { title: active.value.item.title, path: props.crumb ? active.value.item.path : undefined },
  ]
  if (props.crumb) list.push({ title: props.crumb })
  return list
})

const sectionIcons: Record<string, unknown> = {
  Home: Gauge,
  Content: FileText,
  Access: Users,
  System: Settings,
}
const iconFor = (title: string) => sectionIcons[title] ?? FileText

// One-shot messages staged by the server before a redirect (sess.Flash), keyed
// by kind. The session middleware consumes them, so they vanish on the next
// navigation — no dismiss button needed.
const flashVariant = (kind: string) =>
  kind === 'error' ? 'destructive' : kind === 'success' ? 'success' : 'default'
</script>

<template>
  <div class="flex min-h-screen bg-muted/40">
    <!-- Icon rail: one button per section -->
    <aside class="flex w-[4.5rem] shrink-0 flex-col gap-1 border-r bg-background p-2">
      <div
        class="mx-auto mb-3 flex size-9 items-center justify-center rounded-md bg-primary font-bold text-primary-foreground"
      >A</div>
      <a
        v-for="(s, si) in sections"
        :key="s.title"
        :href="s.items[0].path"
        class="flex flex-col items-center gap-1 rounded-md px-1 py-2 text-[11px] leading-none"
        :class="si === active.section
          ? 'bg-accent font-medium text-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-foreground'"
      >
        <component :is="iconFor(s.title)" class="size-[18px]" />
        {{ s.title }}
      </a>
    </aside>

    <!-- Section panel: the active section's items -->
    <aside class="flex w-50 shrink-0 flex-col gap-1 border-r bg-background p-3">
      <div class="px-3 pb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {{ sections[active.section].title }}
      </div>
      <Button
        v-for="item in sections[active.section].items"
        :key="item.path"
        as="a"
        :href="item.path"
        variant="ghost"
        class="w-full justify-start"
        :class="item.path === active.item.path ? 'bg-accent font-medium' : ''"
      >{{ item.title }}</Button>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- Topbar: breadcrumb | theme toggle + user menu -->
      <header class="flex h-14 shrink-0 items-center justify-between gap-4 border-b bg-background px-6">
        <nav aria-label="Breadcrumb" class="flex min-w-0 items-center gap-2 text-sm">
          <template v-for="(c, i) in crumbs" :key="i">
            <span v-if="i > 0" class="text-muted-foreground/50">/</span>
            <a v-if="c.path" :href="c.path" class="text-muted-foreground hover:text-foreground hover:underline">
              {{ c.title }}
            </a>
            <span v-else class="truncate font-medium">{{ c.title }}</span>
          </template>
        </nav>
        <div class="flex items-center gap-1">
          <ThemeToggle />
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="ghost" size="sm">
                {{ user?.username ?? 'account' }}
                <ChevronDown class="size-3.5" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel class="font-normal text-muted-foreground">
                Signed in as <span class="font-medium text-foreground">{{ user?.username }}</span>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <form :action="`${base}/logout`" method="post">
                <DropdownMenuItem as="button" type="submit" class="w-full">
                  <LogOut />
                  Log out
                </DropdownMenuItem>
              </form>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <main class="p-8">
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
  </div>
</template>
```

Note the logout control: it must stay a real form POST. If `DropdownMenuItem as="button" type="submit"` does not submit in the browser (reka-ui intercepts selection), fall back to the same pattern AdminLayout used — a plain `Button type="submit" variant="ghost"` inside the form, styled as a menu row — and record which variant shipped in your report.

- [ ] **Step 2: Migrate `frontend/pages/admin/dashboard.vue`**

```vue
<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number; section?: string }

// Shared props injected by the admin auth middleware on authenticated requests.
defineProps<{
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
    :login-path="loginPath"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="Dashboard" :description="`Signed in as ${adminUser?.username ?? ''}.`" />
    <Card>
      <CardHeader>
        <CardTitle>Getting started</CardTitle>
        <CardDescription>This project scaffolds admin resources.</CardDescription>
      </CardHeader>
      <CardContent class="text-sm text-muted-foreground">
        Generate an admin resource with <code>goappctl gen admin &lt;name&gt;</code>;
        it registers itself in the menu on the left.
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

- [ ] **Step 3: Delete the old layout and update the Owned list**

```bash
git rm frontend/src/components/AdminLayout.vue
```

In `components.go`, the admin `Owned` entry `"frontend/src/components/AdminLayout.vue"` becomes `"frontend/src/components/admin"` (a **replacement**, not an addition).

- [ ] **Step 4: Update `server/ssr_admin_test.go`** — the props at lines 46-52 become:

```go
	html, err := vm.RenderComponent(ctx, "admin/dashboard", map[string]any{
		"adminUser":   map[string]any{"id": 1, "username": "admin"},
		"adminMount":  "/admin",
		"loginPath":   "/admin/login",
		"currentPath": "/admin",
		"adminMenu":   []map[string]any{{"title": "Posts", "path": "/admin/posts", "section": "Content"}},
		"flash":       map[string]string{"success": "Saved"},
	})
```

and the wanted strings gain the shell's chrome:

```go
	for _, want := range []string{"Dashboard", "Posts", "Log out", "Saved", "admin", "Content", "Overview"} {
```

The `maxButtonDepth` and dialog assertions stay exactly as they are — they are the guards this shell must pass.

- [ ] **Step 5: Rebuild the SSR bundle and run the SSR tests**

```bash
pnpm -C frontend run type-check && pnpm -C frontend run build
go test ./server/ -count=1 -v 2>&1 | tail -20
```

Expected: PASS. `TestSSR_GeneratedAdminListRendersUnderQuickJS` still passes too — the fixture page imports `AdminLayout`, which no longer exists… **it does not**: the fixture still references the old path, so `build:ssr` fails or the test fails. That is expected at this point — the fixture is regenerated in Task 8. If `pnpm build` fails on the fixture's import, temporarily verify with `pnpm -C frontend run type-check 2>&1 | grep -v ssrfixture` and proceed: Task 8 is the fix, and the two tasks land in sequence on the same branch. Do not delete or hand-edit the fixture here.

- [ ] **Step 6: Run the Go suite and commit** (`go test ./... -count=1` — the scaffold staleness test compares the fixture against the *template*, both still the old shape, so it passes)

```bash
go build ./... && go vet ./... && gofmt -l ./
git add -A frontend/ cmd/goappctl/internal/components/ server/
git commit -m "feat(admin-ui): AdminShell — two-column menu, breadcrumb, topbar"
```

---

### Task 8: Generated templates on the composites; fixture regenerated

The list template drops from 213 lines to ~70; the form template adopts FormField. The committed SSR fixture is regenerated so QuickJS renders the new shape, and the scaffold assertions follow the new markup.

**Files:**
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/index.vue.tmpl` (full rewrite below)
- Modify: `cmd/goappctl/internal/scaffold/templates/admin/form.vue.tmpl` (full rewrite below)
- Modify: `frontend/pages/admin/ssrfixture/index.vue` (regenerated)
- Modify: `cmd/goappctl/internal/scaffold/admin_test.go` (`TestAdmin_IndexUsesTableAndOverlaysStartClosed`)

**Interfaces:**
- Consumes: every composite from Tasks 5–7, by exact path and props.
- Produces: generated pages whose only table code is column defs.

- [ ] **Step 1: Rewrite `index.vue.tmpl`**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }
interface [[.Type]] { id: number; name: string }

defineProps<{
  items: [[.Type]][]
  basePath: string
  // Injected by the admin auth middleware for the shell:
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  // One-shot messages staged by the handlers before their redirects:
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: 'Name', sortable: true },
]

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<[[.Type]] | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as [[.Type]]
}
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="[[.Type]]">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          New
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
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">Edit</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">Delete</DropdownMenuItem>
          </template>
          <template #empty>No [[.Table]] yet.</template>
        </DataTable>
      </CardContent>
    </Card>

    <!-- Delete posts to the real handler, so the server stays the single source
         of truth — no client-side mutation. -->
    <ConfirmDialog
      :open="pending !== null"
      :title="`Delete “${pending?.name}”?`"
      :action="`${basePath}/${pending?.id}/delete`"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
```

- [ ] **Step 2: Rewrite `form.vue.tmpl`**

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
  item: { id: number; name: string }
  basePath: string
  // Set by the handler only when a submit failed validation: one message per
  // bad field. `item` carries what was typed, so the inputs repopulate on their
  // own — there is no separate `old` prop.
  errors?: Record<string, string>
  // Injected by the admin auth middleware for the shell:
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  // One-shot messages staged by the handlers before their redirects:
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :current-path="currentPath"
    :flash="flash"
    :crumb="item.id ? 'Edit' : 'New'"
  >
    <PageHeader :title="`${item.id ? 'Edit' : 'New'} [[.Type]]`" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form
          :action="item.id ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <FormField name="name" label="Name" :error="errors?.name">
            <Input
              id="name"
              name="name"
              :model-value="item.name"
              :aria-invalid="!!errors?.name"
            />
          </FormField>
          <div class="flex gap-2">
            <Button type="submit">Save</Button>
            <Button as="a" :href="basePath" variant="outline">Cancel</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
```

- [ ] **Step 3: Update the scaffold assertions.** In `TestAdmin_IndexUsesTableAndOverlaysStartClosed`, the table plumbing has moved into `DataTable.vue`, so the wanted strings become:

```go
	for _, want := range []string{
		`import DataTable from '@/components/admin/DataTable.vue'`,
		`import AdminShell from '@/components/admin/AdminShell.vue'`,
		`import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'`,
		`const pending = ref<Post | null>(null)`, // overlay starts closed
		"No post yet.",
		"`${basePath}/${pending?.id}/delete`",
		"`${basePath}/${row.id}/edit`",
		`search-key="name"`,
	} {
```

Keep the two negative checks (`:open="true"` and leftover `[[`/`]]`) exactly as they are. `TestAdmin_WritesFlashOnEveryRedirect`'s `:flash="flash"` assertion still holds for both new templates.

- [ ] **Step 4: Regenerate the fixture** (the regeneration recipe is the fixture's own header comment; the header must be restored because `gen` overwrites the whole file):

```bash
head -21 frontend/pages/admin/ssrfixture/index.vue > /tmp/fixture-header.txt   # the <!-- … --> block
go run ./cmd/goappctl gen admin ssrfixture --force
rm -rf internal/controller/adminssrfixture frontend/pages/admin/ssrfixture/form.vue
git checkout internal/controller/mount_gen.go
cat /tmp/fixture-header.txt frontend/pages/admin/ssrfixture/index.vue > /tmp/fixture.vue \
  && mv /tmp/fixture.vue frontend/pages/admin/ssrfixture/index.vue
```

(Adjust `head -21` to however many lines the comment block actually is — it ends at the first `-->`.)

- [ ] **Step 5: Run everything, SSR included**

```bash
go test ./cmd/goappctl/... -count=1        # staleness + assertion tests
pnpm -C frontend run type-check && pnpm -C frontend run test && pnpm -C frontend run build
go test ./server/ -count=1 -v 2>&1 | tail -15
go test ./... -count=1
```

Expected: all PASS — `TestSSR_GeneratedAdminListRendersUnderQuickJS` renders the new page (its props already include `adminMenu`/`adminMount`; the shell tolerates the missing `currentPath` via its fallback), `maxButtonDepth ≤ 1`, no `role="dialog"`.

- [ ] **Step 6: Commit**

```bash
git add cmd/goappctl/ frontend/pages/admin/ssrfixture/
git commit -m "feat(goappctl): generated admin pages assemble from the composites"
```

---

### Task 9: The written conventions, and the trim proof

**Files:**
- Modify: `README.md` (a "设计约定" subsection inside the existing `## 后台 UI 组件` admin marker block, after the `gen ui` material)
- Test: none new — `go test ./...` plus an `init` trim verification.

- [ ] **Step 1: Add the subsection** (inside the existing `<!--goappctl:admin-->` block — verify with `grep -n "goappctl:" README.md` that the insertion point is inside it):

```markdown
### 设计约定

组件本身就是规范（页面从 `frontend/src/components/admin/` 拼装，改约定就是改组件）；
以下是组件管不住的部分：

- **页面解剖**：`AdminShell` → `PageHeader`（标题 + 右侧动作）→ 卡片。内容区铺满视口；
  唯一例外是表单卡片保持 `max-w-lg` —— 超宽输入框可用性反而差。
- **导航两层封顶**：菜单 = 分组（图标栏）→ 条目（第二栏），由
  `r.Menu(section, title, path)` 注册；更深的层级用面包屑尾巴表达
  （`AdminShell` 的 `crumb` prop），不做菜单嵌套。
- **Dialog 只用于破坏性确认**（`ConfirmDialog`，表单 POST 到真实 handler）；
  新建和编辑一律整页。
- **表格**：行操作收进行尾 "…" 下拉；行的自然链接（名称列）指向编辑页；
  空态文案写业务话（"No posts yet."），不写 "No data"。
- **语义色和主色分工**：绿点/徽章表示启用态、`destructive` 表示危险动作；
  `--primary` 留给每页的主动作。改品牌色只动 `main.css` 的 `--primary`。
- **暗色**：两份 HTML 壳里的启动脚本先于首屏设置 `.dark`，`ThemeToggle` 写
  `localStorage.theme`；组件用令牌（`bg-background` 等），不写死颜色。
```

Also update the intro line of `## 后台 UI 组件` if it still says组件列表 without mentioning `frontend/src/components/admin/` — one sentence noting the composites directory alongside the shadcn copies.

- [ ] **Step 2: Trim proof.** Run the initcmd suite plus a manual dry run and confirm the admin-off story:

```bash
go test ./cmd/goappctl/internal/initcmd/ -count=1 -v 2>&1 | tail -5
go run ./cmd/goappctl init --dry-run 2>&1 | grep -Ei "components/admin|index.html|server/server.go" || true
```

Expected: initcmd PASS; the dry run (with admin off in its combo) lists `frontend/src/components/admin` as deleted and both shells as modified. If the dry-run flags differ, read `go run ./cmd/goappctl init --help` and use the applicable form — the deliverable is the observed output, pasted into your report.

- [ ] **Step 3: Full suite, marker balance, commit**

```bash
go test ./... -count=1
grep -c "goappctl:admin" README.md   # openers; closers counted by the next line
grep -c "goappctl:end" README.md
git add README.md
git commit -m "docs: the admin UI conventions the composites cannot carry"
```

---

## Self-Review Notes

- Spec §3.1–3.6 → Tasks 5–7; §4.1 → Task 2; §4.2 → Task 3; §4.3 → Tasks 1+4; §5 → Task 8; §6 → Task 9; §7 exclusions respected (no mobile work, no multi-field search, no login toggle, no server-side pagination); §8 test matrix → each task's test steps.
- Type consistency: `MenuItem.Section` (Task 2) ↔ shell's `section?: string` (Task 7) ↔ templates' interface (Task 8); `adminUser` map (Task 3) ↔ `{ id?: number; username?: string }` props (Tasks 7–8); `findGroup` three-value returns match across Tasks 3's callers; `Menu("Content", "[[.Type]]", ct.base)` (Task 2 tmpl) ↔ scaffold assertion (Task 2) — Task 8's assertion list intentionally drops it (already pinned in `TestAdmin_RoutesGoThroughTheRegistrar`).
- Known sequencing wart, deliberate: Task 7 leaves the SSR fixture importing the deleted `AdminLayout` until Task 8 regenerates it; `pnpm build` may fail in that window and the plan says so. The two tasks are adjacent on one branch and each carries its own green Go suite.
- The `crumb` prop appears in `form.vue.tmpl` (Task 8) as its first consumer; the dashboard has no tail.
