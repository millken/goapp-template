package scaffold

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MountGenPath is the file holding the generated route wiring, relative to the
// project root.
const MountGenPath = "internal/controller/mount_gen.go"

const (
	regionBegin = "// gen:mounts:begin"
	regionEnd   = "// gen:mounts:end"
	mountSuffix = ".Mount(eng, svc)"
)

// EditMountRegion registers pkg in mount_gen.go: it adds the controller import
// and a Mount call inside the gen:mounts region, keeping the calls sorted.
//
// It reports added=false and returns src unchanged when pkg is already
// registered, so re-running gen for an existing resource is a no-op rather than
// a duplicate route (which the engine would only reject at startup).
func EditMountRegion(src []byte, module, pkg string) ([]byte, bool, error) {
	lines := strings.Split(string(src), "\n")
	begin, end := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case regionBegin:
			begin = i
		case regionEnd:
			end = i
		}
	}
	if begin < 0 || end < 0 || end < begin {
		return nil, false, fmt.Errorf("%s: no %q … %q region found; restore the markers or pass --no-mount",
			MountGenPath, regionBegin, regionEnd)
	}

	// Collect the packages already mounted in the region.
	indent := "\t"
	var mounted []string
	for _, line := range lines[begin+1 : end] {
		trimmed := strings.TrimSpace(line)
		name, ok := strings.CutSuffix(trimmed, mountSuffix)
		if !ok || name == "" {
			continue
		}
		mounted = append(mounted, name)
		indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	}
	if slices.Contains(mounted, pkg) {
		return src, false, nil
	}
	mounted = append(mounted, pkg)
	slices.Sort(mounted)

	region := make([]string, 0, len(mounted))
	for _, name := range mounted {
		region = append(region, indent+name+mountSuffix)
	}

	out := slices.Concat(lines[:begin+1], region, lines[end:])
	out, err := addControllerImport(out, module, pkg)
	if err != nil {
		return nil, false, err
	}

	// gofmt normalizes the import group's order and alignment, so the insertion
	// above only has to be syntactically correct, not tidy.
	formatted, err := format.Source([]byte(strings.Join(out, "\n")))
	if err != nil {
		return nil, false, fmt.Errorf("%s: generated file does not compile after edit: %w", MountGenPath, err)
	}
	return formatted, true, nil
}

// addControllerImport inserts the controller package import into the existing
// import block, next to any sibling controller import so gofmt keeps it in the
// same group.
func addControllerImport(lines []string, module, pkg string) ([]string, error) {
	want := fmt.Sprintf("\t%q", module+"/internal/controller/"+pkg)
	sibling := module + "/internal/controller/"

	at, closeParen := -1, -1
	inImports := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "import (":
			inImports = true
		case inImports && trimmed == ")":
			closeParen = i
			inImports = false
		case inImports && strings.Contains(line, sibling):
			at = i
		}
		if closeParen >= 0 {
			break
		}
	}
	if closeParen < 0 {
		return nil, fmt.Errorf("%s: no parenthesized import block to extend", MountGenPath)
	}
	if at < 0 {
		at = closeParen - 1
	}
	return slices.Insert(lines, at+1, want), nil
}

// AddMount applies EditMountRegion to the project at root.
func AddMount(root, module, pkg string) (bool, error) {
	full := filepath.Join(root, MountGenPath)
	src, err := os.ReadFile(full)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", MountGenPath, err)
	}
	out, added, err := EditMountRegion(src, module, pkg)
	if err != nil {
		return false, err
	}
	if !added {
		return false, nil
	}
	return true, os.WriteFile(full, out, 0o644)
}
