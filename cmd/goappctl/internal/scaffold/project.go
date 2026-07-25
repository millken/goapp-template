package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Project is the layout gen operates on. Shape is detected from directory
// presence rather than a marker file: init leaves no metadata behind, and a
// project's directories are the honest source of truth after hand edits.
type Project struct {
	Root   string
	Module string
	// HasDB reports whether the db component is present. Without it svc.DB is
	// nil at runtime, so generated CRUD has nothing to query.
	HasDB bool
	// HasAdmin reports whether the admin area is present; `gen admin` needs it.
	HasAdmin bool
}

var moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// Detect inspects root and reports what gen can generate there.
func Detect(root string) (Project, error) {
	if root == "" {
		root = "."
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return Project{}, fmt.Errorf("no go.mod in %s — run gen from your project root: %w", root, err)
	}
	m := moduleRe.FindSubmatch(data)
	if m == nil {
		return Project{}, fmt.Errorf("%s/go.mod has no module directive", root)
	}
	if !isDir(filepath.Join(root, "internal/controller")) {
		return Project{}, fmt.Errorf("%s has no internal/controller/ — is this a goapp-template project?", root)
	}
	return Project{
		Root:     root,
		Module:   string(m[1]),
		HasDB:    isDir(filepath.Join(root, "internal/service/db")),
		HasAdmin: isDir(filepath.Join(root, "internal/controller/admin")),
	}, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
