package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
)

// localBackend serves a tree through an *os.Root. Every operation goes through
// the root's methods, so a symlink pointing outside the tree fails in the
// kernel rather than in a string comparison we wrote.
type localBackend struct{ root *os.Root }

// NewLocalBackend wraps an open root. The caller owns the root's lifetime.
func NewLocalBackend(root *os.Root) Backend { return &localBackend{root: root} }

// osName maps the contract's root ("") to the one os understands (".").
func osName(name string) string {
	if name == "" {
		return "."
	}
	return name
}

func (b *localBackend) List(_ context.Context, dir string) ([]Entry, error) {
	f, err := b.root.Open(osName(dir))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			// Only one error is expected here: the entry was deleted between
			// ReadDir and Info, and a listing that reported it would be lying
			// about the present. Anything else — a permission or I/O failure —
			// is returned, because a listing that silently drops entries looks
			// exactly like a directory with fewer files in it.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, entryOf(de.Name(), info))
	}
	return out, nil
}

func (b *localBackend) Stat(_ context.Context, name string) (Entry, error) {
	info, err := b.root.Stat(osName(name))
	if err != nil {
		return Entry{}, err
	}
	return entryOf(info.Name(), info), nil
}

func (b *localBackend) Open(_ context.Context, name string) (io.ReadSeekCloser, error) {
	return b.root.Open(name)
}

func (b *localBackend) Save(_ context.Context, name string, r io.Reader) error {
	f, err := b.root.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (b *localBackend) Mkdir(_ context.Context, dir string) error {
	return b.root.Mkdir(dir, 0o755)
}

func (b *localBackend) Rename(_ context.Context, oldName, newName string) error {
	return b.root.Rename(oldName, newName)
}

func (b *localBackend) Remove(_ context.Context, name string) error {
	// Stat first: RemoveAll reports success for a path that was never there,
	// and semantic 6 requires fs.ErrNotExist so the API can answer 404.
	if _, err := b.root.Stat(osName(name)); err != nil {
		return err
	}
	return b.root.RemoveAll(name)
}

func entryOf(name string, info os.FileInfo) Entry {
	e := Entry{Name: name, IsDir: info.IsDir(), ModTime: info.ModTime()}
	if !e.IsDir {
		e.Size = info.Size()
	}
	return e
}
