package admin

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

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
	c.Set("urlPrefix", a.Storage.URLPrefix())
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

// fmFail maps a storage error to a status and a message. The mapping lives in
// one place so every endpoint answers the same way, and so a new endpoint
// cannot invent its own vocabulary.
func (a *Admin) fmFail(c *inertia.Context, err error) {
	status, msg := http.StatusInternalServerError, "服务器错误"
	switch {
	case errors.Is(err, storage.ErrBadPath), errors.Is(err, storage.ErrRejected):
		status, msg = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, fs.ErrNotExist):
		status, msg = http.StatusNotFound, "不存在"
	case errors.Is(err, fs.ErrExist):
		status, msg = http.StatusConflict, "同名项已存在"
	default:
		// Logged, not returned: the detail may name a filesystem path.
		slog.Error("filemanager", "err", err, "path", c.Request.URL.Path)
	}
	c.Status(status)
	if err := c.JSON(map[string]string{"error": msg}); err != nil {
		slog.Error("filemanager: write error json", "err", err)
	}
}

// Replaced in full by Task 5.
func (a *Admin) fmUpload(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmMkdir(c *inertia.Context)  { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmRename(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmMove(c *inertia.Context)   { c.AbortWithStatus(http.StatusNotImplemented) }
func (a *Admin) fmDelete(c *inertia.Context) { c.AbortWithStatus(http.StatusNotImplemented) }
