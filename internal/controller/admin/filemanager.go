package admin

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/millken/goapp-template/internal/service/storage"
	"github.com/millken/inertia"
)

// mountFileManager registers the media library. Everything goes through the
// registrar, so filemanager.access guards the reads and filemanager.modify the
// writes without either being named here, and the sidebar entry is gated by the
// same key. The section is the registrar's default, "内容": a media library is
// content, not access control.
func (a *Admin) mountFileManager(eng *inertia.Engine) {
	base := a.fileManagerBase()
	r := a.Resource(eng, "filemanager")

	r.GET(base, a.fileManagerPage)
	r.GET(base+"/api/list", a.fmList)
	r.POST(base+"/api/upload", a.fmUpload)
	r.POST(base+"/api/mkdir", a.fmMkdir)
	r.POST(base+"/api/rename", a.fmRename)
	r.POST(base+"/api/move", a.fmMove)
	r.POST(base+"/api/delete", a.fmDelete)
	r.Menu("内容", "文件", base)
}

func (a *Admin) fileManagerBase() string { return a.Prefix() + "/filemanager" }

// fmEntry is the JSON shape of one listing row. It is a DTO rather than
// storage.Entry so the browser gets what it actually needs — a full path and a
// URL — without those becoming part of the storage contract.
type fmEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	URL   string `json:"url"`
}

// fileManagerPage renders the standalone library. The listing itself arrives
// over the JSON API, so this handler ships only what the component needs to
// start talking to it — which is also why it renders under SSR with no data.
func (a *Admin) fileManagerPage(c *inertia.Context) {
	c.Set("basePath", a.fileManagerBase())
	// urlPrefix itself comes from resolve (auth.go), which every route through
	// the registrar passes through — a per-handler copy here would be a second
	// source of truth for the same config value.
	if err := c.Render("admin/filemanager/index"); err != nil {
		slog.Error("render admin filemanager", "err", err)
	}
}

// fmList answers one page of a directory.
func (a *Admin) fmList(c *inertia.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	listing, err := a.Storage.Browse(c.Request.Context(), c.Query("path"), c.Query("q"), page)
	if err != nil {
		a.fmFail(c, err)
		return
	}

	entries := make([]fmEntry, 0, len(listing.Entries))
	for _, e := range listing.Entries {
		full := e.Name
		if listing.Path != "" {
			full = listing.Path + "/" + e.Name
		}
		entries = append(entries, fmEntry{
			Name: e.Name, Path: full, Dir: e.IsDir, Size: e.Size,
			MTime: e.ModTime.Unix(), URL: a.Storage.URLFor(full),
		})
	}

	crumbs := make([]map[string]string, 0, len(listing.Breadcrumb))
	for _, b := range listing.Breadcrumb {
		crumbs = append(crumbs, map[string]string{"name": b.Name, "path": b.Path})
	}

	a.fmOK(c, map[string]any{
		"path":       listing.Path,
		"breadcrumb": crumbs,
		"entries":    entries,
		"total":      listing.Total,
		"page":       listing.Page,
		"pageSize":   listing.PageSize,
	})
}

// fmOK writes a success body with ok:true merged in.
func (a *Admin) fmOK(c *inertia.Context, body map[string]any) {
	body["ok"] = true
	if err := c.JSON(body); err != nil {
		slog.Error("filemanager: write json", "err", err)
	}
}

// fmFail maps a storage error to a status and a message. The status mapping
// lives here, in one place, so every endpoint answers the same way and a new
// endpoint cannot invent its own status vocabulary. The message text itself
// delegates to storage.Reason — the same function Move and Delete use to
// build ItemError.Reason — so there is exactly one place that turns a
// storage error into words, not two independently-maintained copies of the
// same strings.
func (a *Admin) fmFail(c *inertia.Context, err error) {
	status, msg := http.StatusInternalServerError, "服务器错误"
	switch {
	case errors.Is(err, storage.ErrBadPath), errors.Is(err, storage.ErrRejected):
		status, msg = http.StatusUnprocessableEntity, storage.Reason(err)
	case errors.Is(err, fs.ErrNotExist):
		status, msg = http.StatusNotFound, storage.Reason(err)
	case errors.Is(err, fs.ErrExist):
		status, msg = http.StatusConflict, storage.Reason(err)
	default:
		// "服务器错误" stays admin's own rather than storage.Reason's default
		// ("操作失败"): this is the one case whose detail is never shown, only
		// logged, and that is an HTTP-level policy decision (what a client may
		// see on a 500), not a case of the shared vocabulary.
		slog.Error("filemanager", "err", err, "path", c.Request.URL.Path)
	}
	c.Status(status)
	if err := c.JSON(map[string]string{"error": msg}); err != nil {
		slog.Error("filemanager: write error json", "err", err)
	}
}

// fmUpload streams a multipart body straight to storage.
//
// Two failure classes, deliberately kept apart (§5.2). A per-file failure —
// extension, size, an illegal name, or a backend error such as a full disk —
// is an item error: the part is drained, the remaining parts are still
// processed, and the response is 200 with the failure listed. A body-level
// failure — the request ceiling, a malformed body, a dropped connection —
// kills the request, because there are no further parts to report on.
// Collapsing the two into one http.MaxBytesReader would turn "one of your
// five files is too big" into "your upload failed", which is a worse answer
// and a harder one to act on.
//
// The target directory rides in the query string rather than a form field: parts
// arrive in order, and a field placed after the files would be read too late.
func (a *Admin) fmUpload(c *inertia.Context) {
	ctx := c.Request.Context()
	dir := c.Query("path")

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, a.Storage.MaxRequestSize())
	mr, err := c.Request.MultipartReader()
	if err != nil {
		a.fmBodyFail(c, err)
		return
	}

	entries := []fmEntry{}
	fails := []map[string]string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			a.fmBodyFail(c, err)
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			_ = part.Close()
			continue
		}

		name := part.FileName()
		e, err := a.Storage.Upload(ctx, dir, name, part)
		if err != nil {
			// Every error from Upload is this item's alone: a backend failure
			// on one file says nothing about whether the next one will fail
			// too (a rejected extension on file 2 doesn't imply file 3 is
			// also rejected; a permission error on file 2 doesn't imply
			// file 3's write will also fail). The caller learns more from a
			// per-file report than from a batch truncated at the first
			// error, so the loop always continues to NextPart.
			fails = append(fails, map[string]string{"name": name, "error": storage.Reason(err)})
			// part.Close drains whatever of the part was not read, so there
			// is nothing else to do before moving on to the next part.
			_ = part.Close()
			continue
		}
		_ = part.Close()

		full := e.Name
		if d := cleanQueryDir(dir); d != "" {
			full = d + "/" + e.Name
		}
		entries = append(entries, fmEntry{
			Name: e.Name, Path: full, Dir: false, Size: e.Size,
			MTime: e.ModTime.Unix(), URL: a.Storage.URLFor(full),
		})
	}

	a.fmOK(c, map[string]any{"entries": entries, "errors": fails})
}

// cleanQueryDir normalises the upload target for building response paths. The
// service already validated it — anything illegal failed before we got here.
func cleanQueryDir(dir string) string { return strings.Trim(dir, "/") }

// fmBodyFail answers a body-level upload failure. Deliberately no "errors" key:
// a per-item report would claim knowledge of parts that were never read.
func (a *Admin) fmBodyFail(c *inertia.Context, err error) {
	var tooLarge *http.MaxBytesError
	status, msg := http.StatusBadRequest, "上传内容无法解析"
	if errors.As(err, &tooLarge) {
		status, msg = http.StatusRequestEntityTooLarge, "上传内容超过单次请求上限"
	}
	c.Status(status)
	if err := c.JSON(map[string]string{"error": msg}); err != nil {
		slog.Error("filemanager: write error json", "err", err)
	}
}

// fmMkdir creates one directory.
func (a *Admin) fmMkdir(c *inertia.Context) {
	var req struct{ Path, Name string }
	if !a.fmDecode(c, &req) {
		return
	}
	if err := a.Storage.Mkdir(c.Request.Context(), req.Path, req.Name); err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{})
}

// fmRename renames one entry inside its own directory.
func (a *Admin) fmRename(c *inertia.Context) {
	var req struct{ Path, Name string }
	if !a.fmDecode(c, &req) {
		return
	}
	if err := a.Storage.Rename(c.Request.Context(), req.Path, req.Name); err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{})
}

// fmMove relocates entries into another directory.
func (a *Admin) fmMove(c *inertia.Context) {
	var req struct {
		Paths []string `json:"paths"`
		To    string   `json:"to"`
	}
	if !a.fmDecode(c, &req) {
		return
	}
	fails, err := a.Storage.Move(c.Request.Context(), req.Paths, req.To)
	if err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{"errors": itemErrors(fails)})
}

// fmDelete removes entries, recursively for directories.
func (a *Admin) fmDelete(c *inertia.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !a.fmDecode(c, &req) {
		return
	}
	fails, err := a.Storage.Delete(c.Request.Context(), req.Paths)
	if err != nil {
		a.fmFail(c, err)
		return
	}
	a.fmOK(c, map[string]any{"errors": itemErrors(fails)})
}

// fmDecode reads a JSON request body, answering 422 and reporting false when it
// cannot. The 1MB ceiling is for a body of paths; anything larger is not one.
func (a *Admin) fmDecode(c *inertia.Context, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		c.Status(http.StatusUnprocessableEntity)
		if err := c.JSON(map[string]string{"error": "请求格式不正确"}); err != nil {
			slog.Error("filemanager: write error json", "err", err)
		}
		return false
	}
	return true
}

// itemErrors renders per-item failures in the response shape §5.2 fixes.
func itemErrors(in []storage.ItemError) []map[string]string {
	out := make([]map[string]string, 0, len(in))
	for _, e := range in {
		out = append(out, map[string]string{"name": e.Name, "error": e.Reason})
	}
	return out
}
