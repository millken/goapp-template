# Admin UI Conventions — Design Spec

Date: 2026-07-26 · Status: Validated — approved for planning · Depends on: the
`admin` component (shadcn-vue base components, permissions, menu registry)

An interactive preview of everything below was built and approved before this
spec was written: two-column menu, breadcrumb, DataTable behaviour, form and
permission-grid mockups, dark mode. The spec records what the preview showed.

## 1. Goal

Fix the admin UI's look and page anatomy once, so that every later page — stage
2's user management, the group editor, anything `gen admin` produces — is
assembled from parts instead of designed again. "Not having to think about UI"
is implemented as composite components whose use *is* the convention, plus a
short written record of the decisions the components cannot carry.

What exists today: 12 copied shadcn-vue base components, a single-column
sidebar layout, and two generated page templates whose 200-odd lines each
re-state the same table and form plumbing. The conventions live nowhere except
in those files, so each new page re-decides them.

## 2. Decisions

| Question | Decision |
|---|---|
| Form of the standard | **Composites + short doc.** Conventions live in components; the doc records only what components can't express |
| Visual base | **Existing neutral-gray tokens, untouched.** This is a template; one `--primary` edit rebrands it. Dark tokens already exist — add the toggle |
| Shell | **Two-column menu + topbar.** Icon rail (top-level sections) + section panel (items of the active section). Chosen over a multi-level tree for capacity: each section gets a full column of items |
| Topbar | Left: **breadcrumb** (`Section / Item / Subpage`). Right: **ThemeToggle + user menu** (username, log out — moved out of the sidebar) |
| Page title | Stays in the content area's `PageHeader`, not the topbar. The breadcrumb answers "where am I", the PageHeader answers "what is this page" |
| Content width | **Fills the viewport** — no max-width cap. Exception: form cards stay `max-w-lg` for input usability |
| Table plumbing | **`DataTable` composite** wraps the ~150 lines of TanStack pipeline (search / sort / client pagination / empty state). Cells and row actions are slots |
| Dialogs vs pages | Only destructive confirmation uses a dialog (`ConfirmDialog`). Create and edit are always full pages |
| Dark mode | Boot script in both HTML shells (no FOUC), `ThemeToggle` persists to `localStorage.theme`, falls back to `prefers-color-scheme` |
| Menu grouping | `MenuItem` gains a `section`; the registrar's `Menu` takes it. Permission filtering is unchanged — grouping is display-only |

## 3. Composites

All in `frontend/src/components/admin/` — a new directory owned by the `admin`
component (added to its `Owned` list in `components.go`), deleted wholesale by
`init` when admin is off. Like the shadcn base components, they are owned code:
when a page outgrows a composite, edit the composite.

### 3.1 `AdminShell.vue` (replaces `AdminLayout.vue`)

The frame: icon rail + section panel + topbar + content slot + flash alerts.

- **Icon rail** (~4.5rem): one button per menu section, icon above a small
  label. Icons come from `lucide-vue-next` (already a dependency); the section →
  icon mapping is a small record in the shell with a default icon for unknown
  sections.
- **Section panel** (~12.5rem): the active section's items as ghost buttons,
  uppercase section title above. Plain `<a href>` navigation as today — PJAX
  intercepts at the document level.
- **Topbar** (3.5rem): breadcrumb left; `ThemeToggle` and the user dropdown
  (username, "Signed in as", log out form) right.
- **Breadcrumb**: computed client-side from the menu prop and
  `location.pathname` — section (links to its first item) / item / optional
  tail. Pages set the tail (e.g. "Edit carol") via a `crumb` prop on the shell.
  No server-side breadcrumb data.
- **Built-in Home**: the shell itself contributes the Home section with an
  Overview item pointing at the admin mount, exactly as today's hardcoded
  Dashboard button. It is not a registered menu item and needs no permission.
- **Active state**: an item is active when `location.pathname` equals its path
  or extends it with `/` (so `/admin/user/3/edit` lights up Users). The active
  item's section is the active section; on a path no menu item matches, Home.
- Keeps the current props (`menu`, `user`, `mount`, `loginPath`, `flash`) plus
  `crumb?: string`. `user` becomes `{ id, username }` (see §4.2).

`AdminLayout.vue` is deleted, not kept as an alias — the template has three
pages and two templates to migrate, all in this repo.

### 3.2 `PageHeader.vue`

`title`, `description?`, `#actions` slot. `<h1>` at 1.5rem/600, description in
muted foreground, actions right-aligned. Every page starts with one.

### 3.3 `DataTable.vue`

Props:

```ts
{
  columns: { key: string; label: string; sortable?: boolean }[]
  data: Record<string, unknown>[]
  searchKey?: string        // shows the filter input, filters on this field
  pageSize?: number         // default 20
}
```

Slots: `#cell-<key>="{ row }"` overrides a cell (default renders
`row[key]` as text); `#row-actions="{ row }"` fills the trailing actions cell
(rendered inside the ghost "…" `DropdownMenu`, so pages provide only
`DropdownMenuItem`s); `#empty` overrides the empty-state text.

Internals: `useVueTable` with the core/filtered/sorted/pagination row models —
the exact pipeline the current template inlines, including the numbered-page
pagination pattern (the nested-button lesson is pinned by the SSR test and must
survive the move). Column defs are the simple spec above, not raw TanStack
`ColumnDef`; a page needing more edits the composite.

### 3.4 `FormField.vue`

`name`, `label`, `error?`; default slot for the control. Renders label, slot,
and the error line in destructive color; adds the invalid style to the slot's
control via a class on the wrapper. Errors come only from the server's `errors`
prop — no client-side validation, as today.

### 3.5 `ConfirmDialog.vue`

`v-model:open`, `title`, `description?`, `action` (POST target),
`confirmLabel?` (default "Delete"). Renders the Dialog with Cancel +
destructive submit inside a plain `<form method="post" :action>`— the current
delete pattern, extracted. Dialogs start closed; SSR renders no overlay (the
existing `role="dialog"` SSR assertion keeps this honest).

### 3.6 `ThemeToggle.vue`

Icon button; on click toggles `document.documentElement.classList` `dark`,
persists `localStorage.theme = 'dark' | 'light'`. Reads nothing at setup time —
initial state comes from the DOM class the boot script set, read in
`onMounted` — so it is inert under QuickJS SSR.

## 4. Backend touches

### 4.1 Menu sections

```go
// MenuItem gains:
Section string `json:"section"` // display grouping; empty → "Content"

// Registrar (signature change, no back-compat shim — repo-internal API):
func (r *Registrar) Menu(section, title, path string)

// Ungated entries keep AddMenuItem(item MenuItem) — callers set Section.
```

Section display order: first-registration order, items within a section by the
existing `Order`-then-`Title` rule. Permission filtering is untouched — a
section with no visible items is not sent. The generated template calls
`r.Menu("Content", "[[.Type]]", ct.base)`; the string is the user's to edit.

### 4.2 `adminUser` becomes `{id, username}`

The topbar shows a username; the prop currently carries the raw id. `findGroup`
already joins `users` — the same query also selects `u.username`. `findGroup`
returns `(*group, string, error)` (group, username), `resolve` injects
`c.Set("adminUser", map[string]any{"id": id, "username": username})`. Still one
query per request. The session still stores only the id.

### 4.3 Dark-mode boot script

Three lines in the `<head>` of both HTML shells (`frontend/index.html`,
`server/server.go`'s string template):

```html
<script>try{if(localStorage.theme==='dark'||(!('theme' in localStorage)&&matchMedia('(prefers-color-scheme: dark)').matches))document.documentElement.classList.add('dark')}catch(e){}</script>
```

Wrapped in `goappctl:admin` markers in both files. `markers` currently has no
`.html` form — a marker in an unsupported file type makes `init` error — so the
`.html` extension is added to `markers.forms`, using HTML comments (same
delimiters as `.md`).

## 5. Generated templates

- `index.vue.tmpl` → `PageHeader` + `DataTable` + `ConfirmDialog` (~70 lines,
  matching the approved preview's shape). The `[[ ]]` delimiters and the
  `AdminShell` wrapper carry over.
- `form.vue.tmpl` → `FormField` per field inside the `max-w-lg` card.
- `handler.go.tmpl` → `r.Menu("Content", "[[.Type]]", ct.base)`.
- `frontend/pages/admin/dashboard.vue` and the ssrfixture page migrate to the
  new composites; the fixture is regenerated and
  `TestAdminIndexFixtureIsCurrent` keeps it honest.
- `login.vue` is untouched: a standalone card with no shell, it inherits dark
  mode from the boot script and gets no toggle.

## 6. The written conventions (README)

A "设计约定" subsection under the existing 后台 UI 组件 section, one screen,
inside the admin marker block. Contents — only what components can't enforce:

- Page anatomy: `AdminShell` → `PageHeader` → cards. Content fills the width;
  form cards stay `max-w-lg`.
- Navigation: menu = two levels (section → item), registered via
  `r.Menu(section, title, path)`; deeper hierarchy is expressed by breadcrumb
  tails, not menu nesting.
- Dialog vs page: destructive confirmation is the only dialog; create/edit are
  pages.
- Tables: row actions live in the trailing "…" dropdown; a row's natural link
  (the name column) goes to edit.
- Semantic color: green dot/badge for active-state, destructive for dangerous
  actions; the primary color is reserved for the primary action per page.

## 7. Out of scope

- Responsive/mobile admin (the preview's `display:none` breakpoints are not the
  design; small screens are not a target for this template's admin).
- User management and group editor pages themselves — stage 2 builds them *on*
  these conventions.
- Server-side pagination in DataTable (client-side as today; the seam is
  `pageSize` and a later `manual` flag).
- A ThemeToggle on the login page.

## 8. Testing

- **DataTable behaviour** — vitest + happy-dom (already in devDependencies):
  filtering narrows rows, sorting toggles, pagination slices, `#cell-*` slot
  renders, empty state shows. First component-level vitest in the repo, so the
  task includes the minimal vitest config if none exists.
- **SSR** — the regenerated ssrfixture page exercises AdminShell + PageHeader +
  DataTable + ConfirmDialog under QuickJS via the existing
  `TestSSR_GeneratedAdminListRendersUnderQuickJS` (button-nesting and
  no-dialog assertions carry over unchanged).
- **Menu sections** — Go tests: `menuItems` grouping/order, empty-section
  omission, `Menu(section, ...)` recording; the request-level menu prop test
  updated for the new shape.
- **adminUser shape** — the StoreDB regression test asserts the prop carries
  `username`.
- **Trim** — `init --dry-run` shows `frontend/src/components/admin/` deleted
  and both boot scripts stripped; the `.html` markers form gets its own
  markers-package test.
- **Templates** — goappctl scaffold assertions updated (registrar call with
  section, composites imported).
