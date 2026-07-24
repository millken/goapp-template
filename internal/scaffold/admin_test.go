package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdmin_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	if err := Admin("blog-post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("Admin: %v", err)
	}

	want := []string{
		"internal/controller/adminblogpost/handler.go",
		"internal/controller/adminblogpost/model.go",
		"frontend/pages/admin/blog-post/index.vue",
		"frontend/pages/admin/blog-post/form.vue",
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected file %s: %v", rel, err)
		}
	}
}

func TestAdmin_HandlerHasAdminRoutes(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	handler, err := os.ReadFile(filepath.Join(root, "internal/controller/adminpost/handler.go"))
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	got := string(handler)
	for _, want := range []string{`/post`, `admin/post/index`} {
		if !strings.Contains(got, want) {
			t.Errorf("handler missing %q:\n%s", want, got)
		}
	}
}

func TestAdmin_RefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("first Admin: %v", err)
	}
	if err := Admin("post", Options{ModuleRoot: root}); err == nil {
		t.Fatal("expected overwrite error, got nil")
	}
}
