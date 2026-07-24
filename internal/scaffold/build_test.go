package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResource_OutputCompiles generates a public resource into the real module
// tree and compiles it. The generated Controller embeds the real *app.Services
// and its Mount takes the real *inertia.Engine, so a successful build proves the
// generated code wires against the real types (no provider-interface indirection
// to get wrong).
func TestResource_OutputCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skips go build in -short mode")
	}
	root := moduleRoot(t)

	const resource = "genwiresmoke"
	pkgDir := filepath.Join(root, "internal", "controller", resource)
	viewDir := filepath.Join(root, "frontend", "pages", resource)
	t.Cleanup(func() {
		_ = os.RemoveAll(pkgDir)
		_ = os.RemoveAll(viewDir)
	})

	if err := Resource(resource, Options{ModuleRoot: root, Force: true}); err != nil {
		t.Fatalf("Resource: %v", err)
	}
	goBuild(t, root, "./internal/controller/"+resource+"/...")
}

// TestAdmin_OutputCompiles does the same for an admin resource, which also wires
// against the real *admin.Admin (Prefix/AuthMiddleware/AddMenuItem).
func TestAdmin_OutputCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skips go build in -short mode")
	}
	root := moduleRoot(t)

	const resource = "genwiresmoke"
	pkg := "admin" + resource
	pkgDir := filepath.Join(root, "internal", "controller", pkg)
	viewDir := filepath.Join(root, "frontend", "pages", "admin", resource)
	t.Cleanup(func() {
		_ = os.RemoveAll(pkgDir)
		_ = os.RemoveAll(viewDir)
	})

	if err := Admin(resource, Options{ModuleRoot: root, Force: true}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	goBuild(t, root, "./internal/controller/"+pkg+"/...")
}

func goBuild(t *testing.T, dir, pattern string) {
	t.Helper()
	cmd := exec.Command("go", "build", pattern)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated package failed to build:\n%s", out)
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
