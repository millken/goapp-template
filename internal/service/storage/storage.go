package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Sentinels the HTTP layer maps to status codes. ErrBadPath is a malformed or
// escaping path (422); ErrRejected is an upload the policy refuses (422).
// Everything else arrives from a Backend already wrapping fs.ErrNotExist or
// fs.ErrExist.
var (
	ErrBadPath  = errors.New("storage: illegal path")
	ErrRejected = errors.New("storage: rejected")
)

// Defaults applied by the accessors when a field is left empty.
const (
	defaultURLPrefix      = "/uploads"
	defaultMaxUploadSize  = 8 << 20  // 8MB per file
	defaultMaxRequestSize = 64 << 20 // 64MB per upload request
	defaultPageSize       = 40
	// rootCrumbLabel names the root in a breadcrumb; "" would render blank.
	rootCrumbLabel = "全部文件"
)

// defaultAllowedExt deliberately omits .svg: an SVG served from our own origin
// executes script on our own origin. An operator who needs it can list it.
var defaultAllowedExt = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".pdf", ".zip"}

// Config configures the storage service. A pointer in New lets Start
// distinguish "enabled but misconfigured" (nil) from "not enabled".
type Config struct {
	// Root is the browsable tree. Required: every plausible default is a
	// directory the app would then start writing user uploads into by surprise.
	Root string `yaml:"root"`
	// URLPrefix is the public read path (default "/uploads").
	URLPrefix string `yaml:"url_prefix"`
	// MaxUploadSize caps one file (default 8MB).
	MaxUploadSize int64 `yaml:"max_upload_size"`
	// MaxRequestSize caps a whole upload request (default 64MB). Distinct from
	// MaxUploadSize on purpose — see §5.2 of the spec: one is a per-item
	// rejection, the other kills the request.
	MaxRequestSize int64 `yaml:"max_request_size"`
	// PageSize is entries per listing page (default 40).
	PageSize int `yaml:"page_size"`
	// AllowedExt is the upload whitelist, lowercase with dots.
	AllowedExt []string `yaml:"allowed_ext"`
}

// Crumb is one breadcrumb segment.
type Crumb struct {
	Name string
	Path string
}

// Listing is one page of a directory.
type Listing struct {
	Path       string
	Breadcrumb []Crumb
	Entries    []Entry
	Total      int
	Page       int
	PageSize   int
}

// ItemError is one item's failure inside a batch operation.
type ItemError struct {
	Name   string
	Reason string
}

// Service is the storage infrastructure service.
type Service struct {
	cfg  *Config
	root *os.Root
	be   Backend
}

// New constructs the service. Nothing is opened until Start.
func New(cfg *Config) *Service { return &Service{cfg: cfg} }

// Start resolves and creates the root, opens it, and proves it is writable.
func (s *Service) Start(_ context.Context) error {
	if s.cfg == nil {
		return errors.New("storage: service enabled but [storage] config section missing")
	}
	if strings.TrimSpace(s.cfg.Root) == "" {
		return errors.New("storage: root is required")
	}
	abs, err := filepath.Abs(s.cfg.Root)
	if err != nil {
		return fmt.Errorf("storage: resolve root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("storage: create root: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return fmt.Errorf("storage: open root: %w", err)
	}
	// Write probe: a read-only uploads directory is a startup problem, and
	// finding it here beats finding it on a user's first upload.
	const probe = ".storage-write-probe"
	f, err := root.Create(probe)
	if err != nil {
		_ = root.Close()
		return fmt.Errorf("storage: root is not writable: %w", err)
	}
	_ = f.Close()
	_ = root.Remove(probe)

	s.root = root
	s.be = NewLocalBackend(root)
	return nil
}

// Stop closes the root.
func (s *Service) Stop(_ context.Context) error {
	if s.root == nil {
		return nil
	}
	return s.root.Close()
}

// FS exposes the tree for the public static route. It is root.FS(), not
// os.DirFS: the latter would follow a symlink planted inside the tree out of it.
func (s *Service) FS() fs.FS { return s.root.FS() }

func (s *Service) URLPrefix() string { return cmp.Or(s.cfg.URLPrefix, defaultURLPrefix) }

// URLFor is the public URL of a stored path.
func (s *Service) URLFor(name string) string { return s.URLPrefix() + "/" + name }

func (s *Service) MaxRequestSize() int64 {
	return cmp.Or(s.cfg.MaxRequestSize, int64(defaultMaxRequestSize))
}

func (s *Service) maxUploadSize() int64 {
	return cmp.Or(s.cfg.MaxUploadSize, int64(defaultMaxUploadSize))
}

func (s *Service) pageSize() int { return cmp.Or(s.cfg.PageSize, defaultPageSize) }

func (s *Service) allowedExt() []string {
	if len(s.cfg.AllowedExt) == 0 {
		return defaultAllowedExt
	}
	return s.cfg.AllowedExt
}

// ValidatePath is clean with the cleaned value discarded: the exported way for
// another package to ask "is this a legal path inside the tree?" without clean
// itself becoming API. An empty path is legal and means "nothing selected".
func (s *Service) ValidatePath(name string) error {
	_, err := clean(name)
	return err
}

// clean is the single gate every public method passes its input through. It
// returns a slash-separated relative path, or ErrBadPath.
//
// It is strict rather than forgiving on purpose: a path that needs
// interpretation is a path a future reader will interpret differently. Note
// that os.Root would also refuse an escape — this is the first of two layers,
// not the only one.
func clean(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.ContainsAny(p, `\`) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: control character", ErrBadPath)
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "", seg == ".", seg == "..":
			return "", fmt.Errorf("%w: %q", ErrBadPath, p)
		case len(seg) > 255:
			return "", fmt.Errorf("%w: segment too long", ErrBadPath)
		case strings.TrimSpace(seg) == "":
			return "", fmt.Errorf("%w: blank segment", ErrBadPath)
		}
	}
	return p, nil
}

// join cleans a directory and a base name into one path.
func join(dir, name string) (string, error) {
	d, err := clean(dir)
	if err != nil {
		return "", err
	}
	if _, err := clean(name); err != nil {
		return "", err
	}
	if d == "" {
		return name, nil
	}
	return d + "/" + name, nil
}

// Browse returns one page of dir, filtered and sorted.
func (s *Service) Browse(ctx context.Context, dir, query string, page int) (Listing, error) {
	d, err := clean(dir)
	if err != nil {
		return Listing{}, err
	}
	entries, err := s.be.List(ctx, d)
	if err != nil {
		return Listing{}, err
	}

	if q := strings.ToLower(strings.TrimSpace(query)); q != "" {
		entries = slices.DeleteFunc(entries, func(e Entry) bool {
			return !strings.Contains(strings.ToLower(e.Name), q)
		})
	}
	slices.SortFunc(entries, func(x, y Entry) int {
		if x.IsDir != y.IsDir {
			if x.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})

	size := s.pageSize()
	if page < 1 {
		page = 1
	}
	total := len(entries)
	start := min((page-1)*size, total)
	out := entries[start:min(start+size, total)]

	return Listing{
		Path:       d,
		Breadcrumb: breadcrumb(d),
		Entries:    out,
		Total:      total,
		Page:       page,
		PageSize:   size,
	}, nil
}

// breadcrumb turns "a/b" into root, a, a/b.
func breadcrumb(dir string) []Crumb {
	out := []Crumb{{Name: rootCrumbLabel, Path: ""}}
	if dir == "" {
		return out
	}
	acc := ""
	for _, seg := range strings.Split(dir, "/") {
		if acc == "" {
			acc = seg
		} else {
			acc += "/" + seg
		}
		out = append(out, Crumb{Name: seg, Path: acc})
	}
	return out
}

// Stat reports one entry, for callers that need to know a path exists.
func (s *Service) Stat(ctx context.Context, name string) (Entry, error) {
	n, err := clean(name)
	if err != nil {
		return Entry{}, err
	}
	return s.be.Stat(ctx, n)
}

// Upload stores one file under dir, applying the whole policy: extension
// whitelist, filename sanitisation, collision renaming, and the per-file size
// cap. r is read at most MaxUploadSize+1 bytes; exceeding that removes what was
// written and returns ErrRejected, because a half-file is worse than no file.
func (s *Service) Upload(ctx context.Context, dir, filename string, r io.Reader) (Entry, error) {
	d, err := clean(dir)
	if err != nil {
		return Entry{}, err
	}
	name := sanitiseFilename(filename)
	if name == "" {
		return Entry{}, fmt.Errorf("%w: 文件名为空", ErrRejected)
	}
	ext := strings.ToLower(path.Ext(name))
	if !slices.Contains(s.allowedExt(), ext) {
		return Entry{}, fmt.Errorf("%w: 不接受的文件类型 %q", ErrRejected, ext)
	}

	name, err = s.freeName(ctx, d, name)
	if err != nil {
		return Entry{}, err
	}
	full, err := join(d, name)
	if err != nil {
		return Entry{}, err
	}

	max := s.maxUploadSize()
	limited := &io.LimitedReader{R: r, N: max + 1}
	if err := s.be.Save(ctx, full, limited); err != nil {
		// Save may have written part of the file before failing (disk full,
		// permission loss mid-copy). Best-effort cleanup: the remove error is
		// discarded because the Save error is the one the caller needs, and
		// there is nothing more to do if the cleanup itself fails.
		_ = s.be.Remove(ctx, full)
		return Entry{}, err
	}
	if limited.N == 0 { // read max+1 bytes: the file is over the cap
		_ = s.be.Remove(ctx, full)
		return Entry{}, fmt.Errorf("%w: 文件超过 %d 字节", ErrRejected, max)
	}
	return s.be.Stat(ctx, full)
}

// freeName returns name, or name-2/name-3… when taken.
func (s *Service) freeName(ctx context.Context, dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		full, err := join(dir, candidate)
		if err != nil {
			return "", err
		}
		if _, err := s.be.Stat(ctx, full); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

// sanitiseFilename reduces a client-supplied name to a safe base name. Unicode
// letters survive — a Chinese filename is legitimate — but separators, control
// characters and leading dots do not.
func sanitiseFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r):
			// dropped
		case unicode.IsSpace(r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".-")
}

// Mkdir creates one directory inside dir.
func (s *Service) Mkdir(ctx context.Context, dir, name string) error {
	if sanitiseFilename(name) != name || name == "" {
		return fmt.Errorf("%w: 目录名不合法", ErrBadPath)
	}
	full, err := join(dir, name)
	if err != nil {
		return err
	}
	return s.be.Mkdir(ctx, full)
}

// Rename renames one entry within its own directory. newName is a base name:
// accepting a path here would make "rename" a silent move.
func (s *Service) Rename(ctx context.Context, name, newName string) error {
	n, err := clean(name)
	if err != nil {
		return err
	}
	if n == "" {
		return fmt.Errorf("%w: 不能重命名根目录", ErrBadPath)
	}
	if sanitiseFilename(newName) != newName || newName == "" {
		return fmt.Errorf("%w: 新名称不合法", ErrBadPath)
	}
	// path.Dir("a.png") is ".": this package spells the root "", and join
	// would otherwise reject "." as an illegal segment before target is built.
	dir := path.Dir(strings.TrimSuffix(n, "/"))
	if dir == "." {
		dir = ""
	}
	target, err := join(dir, newName)
	if err != nil {
		return err
	}
	return s.be.Rename(ctx, n, target)
}

// Move relocates entries into toDir, reporting per-item failures rather than
// stopping at the first. The returned error is a whole-request failure.
func (s *Service) Move(ctx context.Context, names []string, toDir string) ([]ItemError, error) {
	dst, err := clean(toDir)
	if err != nil {
		return nil, err
	}
	if dst != "" {
		if e, err := s.be.Stat(ctx, dst); err != nil {
			return nil, err
		} else if !e.IsDir {
			return nil, fmt.Errorf("%w: 目标不是目录", ErrBadPath)
		}
	}

	var fails []ItemError
	for _, raw := range names {
		n, err := clean(raw)
		if err != nil || n == "" {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		target, err := join(dst, path.Base(n))
		if err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		if err := s.be.Rename(ctx, n, target); err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: reason(err)})
		}
	}
	return fails, nil
}

// Delete removes entries, reporting per-item failures. The root is refused.
func (s *Service) Delete(ctx context.Context, names []string) ([]ItemError, error) {
	var fails []ItemError
	for _, raw := range names {
		n, err := clean(raw)
		if err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: "路径不合法"})
			continue
		}
		if n == "" {
			fails = append(fails, ItemError{Name: raw, Reason: "不能删除根目录"})
			continue
		}
		if err := s.be.Remove(ctx, n); err != nil {
			fails = append(fails, ItemError{Name: raw, Reason: reason(err)})
		}
	}
	return fails, nil
}

// SetMaxUploadSizeForTest and SetMaxRequestSizeForTest lower the caps from a
// test. They exist because the two limits have different failure shapes (§5.2)
// and proving that needs both to be reachable; production sets them in config.
func (s *Service) SetMaxUploadSizeForTest(n int64)  { s.cfg.MaxUploadSize = n }
func (s *Service) SetMaxRequestSizeForTest(n int64) { s.cfg.MaxRequestSize = n }

// reason turns a backend error into a message safe to hand a browser.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "不存在"
	case errors.Is(err, fs.ErrExist):
		return "同名项已存在"
	default:
		return "操作失败"
	}
}
