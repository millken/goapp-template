// Package storage is the file-storage infrastructure service: a rooted tree of
// uploaded files behind a swappable Backend, with local disk as the only
// implementation. It implements app.Lifecycle — Start opens the root, Stop
// closes it — and imports no other component.
package storage

import (
	"context"
	"io"
	"time"
)

// Entry is one item in a directory listing.
type Entry struct {
	Name    string
	IsDir   bool
	Size    int64 // 0 for directories
	ModTime time.Time
}

// Backend is the storage contract. Its semantics are numbered in §4.2 of the
// design spec and made executable by the backendtest package — a backend that
// has not been run against that suite has not implemented this interface.
//
// In brief, and normatively:
//
//  1. Paths are cleaned, slash-separated and relative; "" is the root. Service
//     guarantees this, so a backend must not re-derive it as security.
//  2. Directories are real entities: Mkdir then List shows them, even empty.
//  3. Save overwrites. Not-overwriting is Service's policy, not a backend's.
//  4. Remove deletes whatever is at name, recursively — a file, an empty
//     directory, or a populated one. A missing name is fs.ErrNotExist.
//  5. Open returns a ReadSeekCloser: http.ServeContent needs Seek.
//  6. No atomicity and no locking. Errors wrap fs.ErrNotExist / fs.ErrExist so
//     callers can map status codes with errors.Is and nothing else.
//  7. Rename refuses an existing destination. Unlike Save (semantic 3), an
//     overwrite here is not a policy Service applies on top — os.Root.Rename
//     is renameat(2), which clobbers silently, and a copy+delete backend
//     (an S3-style object store) does too, so every implementation must
//     check first and return fs.ErrExist rather than destroy what was there.
type Backend interface {
	List(ctx context.Context, dir string) ([]Entry, error)
	Stat(ctx context.Context, name string) (Entry, error)
	Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
	Save(ctx context.Context, name string, r io.Reader) error
	Mkdir(ctx context.Context, dir string) error
	Rename(ctx context.Context, oldName, newName string) error
	Remove(ctx context.Context, name string) error
}
