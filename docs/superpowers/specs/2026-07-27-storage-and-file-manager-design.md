# Storage Component and Admin File Manager — Design Spec

Date: 2026-07-27 · Status: Validated — approved for planning · Depends on: the
`admin` component for the UI half (and therefore transitively on `session` and
`db`); the service half depends on nothing

The fifth optional component, and the first one whose whole reason to exist is a
screen. `db` and `session` are infrastructure that features happen to use;
`storage` is infrastructure plus the OpenCart-style file manager that makes it
visible — a browsable media library, and a picker that fills an image field on a
form.

## 1. Goal

Give the template a place to put uploaded files, a way to manage them, and one
real consumer so neither half is speculative.

Three deliverables, in dependency order:

1. **`internal/service/storage`** — a `Lifecycle` service over a rooted
   directory tree, behind a `Backend` interface with local disk as the only
   implementation.
2. **The file manager** — an admin page and a modal picker, both rendering the
   same component, both driven by a JSON API: browse, upload, mkdir, rename,
   move, delete, search, paginate.
3. **A consumer** — an avatar on the admin user, so the image field is exercised
   by code that ships rather than by a fixture.

## 2. Decisions

| Question | Decision |
|---|---|
| Swappable backend now, or concrete local type | **`Backend` interface now.** Overrides the default this template usually takes (see `no cache component` — an interface with one implementation writes its semantics from imagination). Accepted deliberately, with §4.2's contract and §7.1's shared conformance suite as the price: the semantics are written down and executable, so a second implementation is checked against something real |
| How files reach the browser | **One public static route**, `/uploads/*`. Images exist to be shown on public pages; putting reads behind admin auth would make the library useless to the front end |
| Server-side thumbnails | **No.** Considered and cut: `<img>` on the original, sized by CSS, `loading="lazy"`. It costs bandwidth on image-heavy directories and buys back an image-scaling dependency, a cache directory, and an invalidation rule. Revisit when a real directory is slow, not before |
| SVG uploads | **Not in the default whitelist.** An SVG served from our own origin executes script on our own origin. An operator can add `.svg` to `allowed_ext`; the default will not hand them a stored-XSS vector |
| Page vs modal data | **JSON endpoints, one shared component.** Directory changes are widget state, not navigation — routing them through Inertia would put every `cd` in the browser history |
| Where the admin half lives | Inside the `admin` area, wrapped in `goappctl:storage` blocks. `storage` alone is legal (service + public route, no UI); `admin` alone is legal (no file manager) |
| Traversal defence | **`os.Root` plus string cleaning.** Two layers on purpose: the cleaning code can be edited wrong, the kernel-level API cannot |
| Consumer | **`users.avatar`.** A column, a form field, a list cell, and the shell's avatar |

## 3. Component boundary

`storage` is the fifth entry in `cmd/goappctl/internal/components/components.go`.
That file says to extract an abstraction when a fifth component lands; this spec
**does not** do that — it adds one more literal. The note stays true and the
refactor stays available; doing it here would mix a mechanical change into a
feature branch.

```go
{
    Name: "storage",
    // No Deps: the service and its public route stand alone.
    Owned: []string{
        "internal/service/storage",
        // Admin-side files. These sit under directories `admin` also owns;
        // deletion is idempotent, and listing them here is what makes
        // "admin on, storage off" strip correctly.
        "internal/controller/admin/filemanager.go",
        "internal/controller/admin/filemanager_test.go",
        "frontend/pages/admin/filemanager",
        "frontend/src/components/admin/FileManager.vue",
        "frontend/src/components/admin/FileManagerDialog.vue",
        "frontend/src/components/admin/ImagePicker.vue",
        // plus their .test.ts siblings
    },
},
```

Marker blocks, all `goappctl:storage`:

- `internal/app/services.go` — the `Storage *storage.Service` field. Same rule
  as `Session`: only the storage and admin areas may reference it.
- `commands/serve.go` — the Start block, and the public static route.
- `internal/config/config.go` + `config.example.yaml` — the `[storage]` section.
- `internal/controller/admin/admin.go` — the `a.mountFileManager(eng)` call.
- `internal/controller/admin/auth.go` — the `canBrowseFiles` prop (§5.3).
- `internal/controller/admin/user_crud.go` — the avatar column and form field.
- `frontend/pages/admin/user/*.vue` — the avatar cell and the picker.

`FormField.vue` gets **no marker**. It is already "label + slotted control +
error", and the picker is simply a control that goes in the slot — so the shared
file does not learn about storage at all.

`006_user_avatar.up.sql` is **not** marked. A column nobody writes to is
harmless; a conditionally-numbered migration is not.

## 4. The storage service

```
internal/service/storage/
  storage.go   Config, Service (Lifecycle), path cleaning, upload policy
  backend.go   Backend, Entry, sentinel errors
  local.go     the os.Root implementation
  backendtest/ the conformance suite (§7.1)
```

### 4.1 Config

```yaml
#goappctl:storage
storage:
  root: uploads # required — the browsable tree
  url_prefix: /uploads # public read path
  # max_upload_size: 8388608     # bytes, default 8MB
  # page_size: 40                # entries per page in the file manager
  # allowed_ext: [".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".zip"]
#goappctl:end
```

`root` is required: `Validate` fails on an empty one rather than defaulting,
because every default anyone would pick (`.`, the CWD, a temp dir) is a
directory the app would then start writing user uploads into by surprise. A
missing `[storage]` section is the usual `storage: service enabled but [storage]
config section missing`. Everything else defaults through `cmp.Or` accessors, as
`admin.Config` does.

Note `.svg` is absent from the default whitelist (§2), and the config comment
says why.

### 4.2 The Backend contract

```go
type Entry struct {
    Name    string    // base name, no separators
    IsDir   bool
    Size    int64     // 0 for directories
    ModTime time.Time
}

type Backend interface {
    List(ctx context.Context, dir string) ([]Entry, error)
    Stat(ctx context.Context, name string) (Entry, error)
    Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
    Save(ctx context.Context, name string, r io.Reader) error
    Mkdir(ctx context.Context, dir string) error
    Rename(ctx context.Context, oldName, newName string) error
    Remove(ctx context.Context, name string) error
}
```

Six semantics that are part of the interface, not of the local implementation.
They are what a second implementation has to reproduce, and §7.1 makes each one
a test:

1. **Paths are cleaned, slash-separated, and relative.** `""` is the root.
   Backends never see `..`, a leading `/`, a backslash, or a control character —
   `Service` rejects those before calling (§4.4). A backend may assume this and
   must not re-derive it as security.
2. **Directories are real entities.** `Mkdir` then `List` on the parent must show
   the directory, even empty. An object store has to emulate this with a
   zero-byte marker object. Known cost, chosen so the UI can have empty folders.
3. **`Save` overwrites.** Not-overwriting is a policy, and it lives in `Service`
   (§4.4), which renames before calling.
4. **`Remove` on a directory is recursive.**
5. **`Open` returns a `ReadSeekCloser`**, because `http.ServeContent` needs Seek
   for Range and conditional requests. A backend without native seeking must
   wrap.
6. **No atomicity, no cross-operation locking.** Two concurrent writers to one
   name are last-writer-wins. Errors wrap `fs.ErrNotExist` / `fs.ErrExist` so
   callers can map status codes with `errors.Is` and nothing else.

Plus one sentinel of our own, `ErrBadPath`, returned by cleaning (§4.4) — never
by a backend.

### 4.3 The local implementation

`local.go` holds an `*os.Root` opened in `Start`. Every operation goes through
its methods (`root.Open`, `root.Create`, `root.Mkdir`, `root.Rename`,
`root.RemoveAll`, `root.Stat`), so a symlink pointing outside the tree fails at
the syscall, not at a string comparison we wrote. `Service.FS()` returns
`root.FS()` for the public route — `os.DirFS` would not survive a symlink
planted inside the uploads tree.

### 4.4 What Service adds on top

```go
func New(cfg *Config) *Service
func (s *Service) Start(ctx) error   // resolve abs root, MkdirAll, OpenRoot, write-probe
func (s *Service) Stop(ctx) error    // close the root
func (s *Service) FS() fs.FS         // for the public static route
func (s *Service) URLPrefix() string
func (s *Service) URLFor(name string) string

type Listing struct {
    Path       string
    Breadcrumb []Crumb
    Entries    []Entry
    Total, Page, PageSize int
}

func (s *Service) Browse(ctx, dir, query string, page int) (Listing, error)
func (s *Service) Upload(ctx, dir, filename string, r io.Reader) (Entry, error)
func (s *Service) Mkdir(ctx, dir, name string) error
func (s *Service) Rename(ctx, name, newName string) error   // same dir

// Batch: the error is a whole-request failure (an unusable target directory);
// per-item failures come back in ItemErrors, matching §5.2's response shape.
type ItemError struct{ Name, Reason string }
func (s *Service) Move(ctx, names []string, toDir string) ([]ItemError, error)
func (s *Service) Delete(ctx, names []string) ([]ItemError, error)
```

**Cleaning** (`clean(p) (string, error)`) is the single gate. It rejects absolute
paths, any `..` segment, backslashes, control characters, empty segments, and
segments over 255 bytes; it normalises to `path.Clean` with no leading or
trailing slash. Everything public calls it first. `Delete` additionally refuses
the root.

**Upload policy**, in order: extension lowercased and checked against
`allowed_ext`; the client filename reduced to its base and sanitised (path
separators and control characters out, runs of whitespace to `-`, Unicode
letters kept — Chinese filenames are legitimate); a collision resolved by
appending `-2`, `-3` … before the extension; the size capped by
`http.MaxBytesReader` at the handler (§5.2) so the cap is enforced before bytes
are written, not after.

**Browse** lists the directory, filters by case-insensitive substring on the name
when `query` is set (current directory only — not recursive), sorts directories
first then by name case-insensitively, and slices out the page. The whole
directory is read and then filtered, which is O(n) per request; acceptable, and
named in §8.

## 5. HTTP layer

### 5.1 Public reads

```go
//goappctl:storage
eng.GET(stor.URLPrefix()+"/*", inertia.StaticFileServer(stor.URLPrefix(), stor.FS()))
//goappctl:end
```

Not `eng.StaticFS`: that helper returns early in development mode
(`engine.go:303`) because dist is Vite's job in dev — but uploads must be
readable in both modes. Registering the route directly is also what makes it win
over the dist `/*`: the router keeps the deepest wildcard it passed while
descending, and a registered route is matched before the dev-server proxy
fallback.

### 5.2 The admin API

All of it through `Registrar`, so the permission keys and the sidebar entry come
for free — the entry is `r.Menu("内容", "文件", base)`, the registrar's default
section, since a media library is content rather than access control:

| Route | Key |
|---|---|
| `GET /admin/filemanager` (Inertia page) | `filemanager.access` |
| `GET /admin/filemanager/api/list?path=&q=&page=` | `filemanager.access` |
| `POST /admin/filemanager/api/upload` (multipart) | `filemanager.modify` |
| `POST /admin/filemanager/api/mkdir` | `filemanager.modify` |
| `POST /admin/filemanager/api/rename` | `filemanager.modify` |
| `POST /admin/filemanager/api/move` | `filemanager.modify` |
| `POST /admin/filemanager/api/delete` | `filemanager.modify` |

`rename` (one entry, same directory) and `move` (many entries, new directory)
are separate endpoints over the same `Backend.Rename`, because the two UI
gestures differ and folding them into one endpoint would make the payload a
discriminated union for no gain.

**CSRF** rides in `X-CSRF-Token` on every POST — the header `csrf.go` describes
as "for a future JSON client". This has a consequence worth stating: `validCSRF`
reads the header *first* and only falls back to `PostFormValue`, so a request
carrying the header leaves the body unread. That is what lets the upload handler
take `c.Request.MultipartReader()` and stream parts straight to `Save`, instead
of `ParseMultipartForm` buffering 32MB and spilling to temp files. The token
comes from the page's `csrfToken` prop, which `resolve` already sets on every
admin page.

**Responses.** Success is `{"ok":true, …}`. Failure is a 4xx with
`{"error":"…"}`: `fs.ErrNotExist` → 404, `fs.ErrExist` → 409, `ErrBadPath` and
policy rejections (extension, size, empty name) → 422, and anything else 500
with the detail logged rather than returned.

**The batch endpoints are the exception**: an upload where two files succeed and
one is rejected is a normal outcome, not an error. `upload`, `move` and `delete`
therefore answer 200 with
`{"ok":true,"entries":[…],"errors":[{"name":"a.exe","error":"…"}]}` — they act on
every item they can and report the failures per item, and the UI reports both
halves. Nothing is rolled back, consistently with §4.2's no-atomicity rule.

### 5.3 Permission fallout, and the degraded picker

The picker opens inside a *user* page but calls *filemanager* endpoints. An
administrator with `user.modify` and no `filemanager.access` would get a 403 on
a button we drew for them.

So `resolve` (which already sets `adminMenu`, `adminUser`, `csrfToken`) also
sets, inside a `goappctl:storage` block:

```go
c.Set("canBrowseFiles", g.Superuser || g.Permissions.Allows("filemanager"+verbAccess))
```

`ImagePicker` reads it and, when false, degrades to a plain text input for the
path. Reduced capability, not a button that is guaranteed to fail.

## 6. Frontend

```
components/admin/FileManager.vue        breadcrumb + toolbar + grid + pager
components/admin/FileManagerDialog.vue  Dialog wrapper
components/admin/ImagePicker.vue        preview + choose/clear + hidden input
pages/admin/filemanager/index.vue       PageHeader + FileManager
```

`FileManager.vue` has one behavioural prop, `mode`, with three values:

- `manage` — the full page. Multi-select, all mutations, no selection callback.
- `pick` — inside the dialog. Clicking a file emits `select` and closes.
- `dirs` — directories only, used by the "move to…" dialog, which is the same
  component again rather than a fourth thing to build.

Directory changes are component state. Nothing touches the URL, so the browser
back button still means "leave this page", which is what a user pressing it
inside a modal actually wants.

Existing `components/ui` pieces cover it: `dialog`, `pagination`, `button`,
`input`, `table`, `dropdown-menu`. No `gen ui` additions needed.

**The consumer.** Migration `006_user_avatar` adds
`avatar TEXT NOT NULL DEFAULT ''` to `users`. The user form gets

```vue
<FormField name="avatar" label="头像" :error="errors.avatar">
  <ImagePicker name="avatar" v-model="form.avatar" :can-browse="canBrowseFiles" />
</FormField>
```

the user list gets an avatar cell, and `AdminShell` shows the signed-in user's
avatar. Server-side, `avatar` is validated as a clean relative path under the
storage root (reusing `clean`) — a form field that writes a string into a
database is not a reason to trust the string.

## 7. Testing

### 7.1 A conformance suite is how the interface earns its keep

`internal/service/storage/backendtest` exports
`Run(t *testing.T, newBackend func(t *testing.T) Backend)`, and `local_test.go`
calls it. It is one test per numbered semantic in §4.2 — empty directory
survives `List`, `Save` overwrites, `Remove` recurses, `Open` seeks, missing
paths wrap `fs.ErrNotExist`, existing paths wrap `fs.ErrExist`. Writing an S3
backend later means running this suite, not rereading this document.

This is the mitigation for §2's accepted risk. Without it the interface is one
implementation's shape with an `interface` keyword in front.

### 7.2 The rest

- **Path safety** — a table over traversal attempts (`..`, `/etc/passwd`,
  `a/../../b`, backslashes, NUL, over-long segments), plus a symlink planted in
  the tree that points outside it, asserting `os.Root` refuses rather than
  asserting the cleaner caught it.
- **Upload policy** — extension whitelist, size cap trips at the cap, collision
  produces `name-2.png`, a filename of `../../x.png` lands as `x.png`.
- **Browse** — directories first, case-insensitive sort, substring filter,
  page boundaries, and that `total` counts filtered entries.
- **Handlers** — the existing admin test pattern (httptest + a temp SQLite DB):
  the 403 matrix for `access` vs `modify`, a POST without the CSRF header
  rejected, the error→status mapping table, and the partial-upload body.
- **Frontend** — vitest alongside `DataTable.test.ts`: breadcrumb from a path,
  selection emit in `pick` mode, pager arithmetic, and `ImagePicker`
  degrading to a text input when `canBrowse` is false.
- **SSR** — the new admin page joins the QuickJS render list in
  `server/ssr_admin_test.go`. It renders under SSR with an empty listing; the
  data arrives client-side.
- **goappctl** — `storage` off strips the field, blocks, and files; `storage` on
  with `admin` off leaves a compiling project with the service and no UI; the
  closure test gains the new name.

## 8. Known trade-offs

- **No thumbnails.** An image-heavy directory downloads full-size images into a
  grid. Bandwidth, not correctness.
- **No SVG by default.** An operator who needs it opts in and owns the risk.
- **No trash.** Delete is delete. A confirm dialog is the only guard, which is
  what OpenCart does too.
- **Whole-directory read per listing.** Fine for thousands of entries, not for
  hundreds of thousands.
- **Local disk means one machine.** Two app instances need a shared volume or a
  second `Backend`. This is exactly the axis the interface exists for.
- **Last-writer-wins.** Two admins uploading the same name at the same moment
  produce one file, not an error.

## 9. Out of scope

- `gen admin` emitting an image field type for generated resources. The hand-
  written path is proven first; the generator follows in its own spec.
- Image editing, cropping, or metadata.
- Per-user or per-group directory scoping — every admin who can reach the file
  manager sees the whole tree.
- Extracting the component registry abstraction that `components.go` invites at
  the fifth component (§3).
