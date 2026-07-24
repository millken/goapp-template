package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResource_OutputCompilesAndWires generates a public resource into the real
// module tree and compiles it with a wiring assertion (New((*db.Module)(nil))).
//
// A plain build of the generated package is NOT enough: it defines its own
// dbProvider interface and compiles in isolation even if that interface is
// wrong. The type mismatch only surfaces where a real *db.Module is passed to
// New — exactly what the generated wiring hint tells the user to write.
func TestResource_OutputCompilesAndWires(t *testing.T) {
	if testing.Short() {
		t.Skip("skips go build in -short mode")
	}
	root := moduleRoot(t)

	const resource = "genwiresmoke"
	pkgDir := filepath.Join(root, "internal", "module", resource)
	viewDir := filepath.Join(root, "frontend", "pages", resource)
	t.Cleanup(func() {
		_ = os.RemoveAll(pkgDir)
		_ = os.RemoveAll(viewDir)
	})

	if err := Resource(resource, Options{ModuleRoot: root, Force: true}); err != nil {
		t.Fatalf("Resource: %v", err)
	}
	writeWireCheck(t, pkgDir, resource,
		[]string{"github.com/millken/goapp-template/internal/module/db"},
		"New((*db.Module)(nil))")
	goBuild(t, root, "./internal/module/"+resource+"/...")
}

// TestAdmin_OutputCompilesAndWires does the same for an admin resource.
func TestAdmin_OutputCompilesAndWires(t *testing.T) {
	if testing.Short() {
		t.Skip("skips go build in -short mode")
	}
	root := moduleRoot(t)

	const resource = "genwiresmoke"
	pkg := "admin" + resource
	pkgDir := filepath.Join(root, "internal", "module", pkg)
	viewDir := filepath.Join(root, "frontend", "pages", "admin", resource)
	t.Cleanup(func() {
		_ = os.RemoveAll(pkgDir)
		_ = os.RemoveAll(viewDir)
	})

	if err := Admin(resource, Options{ModuleRoot: root, Force: true}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	writeWireCheck(t, pkgDir, pkg,
		[]string{
			"github.com/millken/goapp-template/internal/module/db",
			"github.com/millken/goapp-template/internal/module/admin",
		},
		"New((*db.Module)(nil), (*admin.Module)(nil))")
	goBuild(t, root, "./internal/module/"+pkg+"/...")
}

// writeWireCheck drops a package-level assertion file that forces the generated
// New to accept the real module(s) it is meant to be wired with, so a broken
// provider interface fails the build here instead of in the user's serve.go.
func writeWireCheck(t *testing.T, pkgDir, pkg string, imports []string, newCall string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\nimport (\n")
	for _, imp := range imports {
		b.WriteString("\t" + `"` + imp + `"` + "\n")
	}
	b.WriteString(")\n\nvar _ = " + newCall + "\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "zz_wire_check.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write wire check: %v", err)
	}
}

func goBuild(t *testing.T, dir, pattern string) {
	t.Helper()
	cmd := exec.Command("go", "build", pattern)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated package failed to build/wire:\n%s", out)
	}
}

// moduleRoot returns the directory containing go.mod, or skips the test if not
// running inside a module.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Skip("not running inside a module")
	}
	return filepath.Dir(gomod)
}
