package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdmin_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	if err := Admin("blog-post", Options{ModuleRoot: root, Module: testModule}); err != nil {
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
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
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

// TestAdmin_WritesFlashOnEveryRedirect guards the reason flash exists: a
// redirect discards every prop the handler set, so each write must stage a
// message or the generated CRUD gives no feedback at all.
func TestAdmin_WritesFlashOnEveryRedirect(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	handler, err := os.ReadFile(filepath.Join(root, "internal/controller/adminpost/handler.go"))
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	got := string(handler)
	for _, want := range []string{
		`ct.flash(c, "success", "Post created")`,
		`ct.flash(c, "success", "Post updated")`,
		`ct.flash(c, "success", "Post deleted")`,
		`sess.Flash(kind, message)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("handler missing %q:\n%s", want, got)
		}
	}

	// The shell renders the prop, so the pages have to pass it down.
	for _, rel := range []string{"frontend/pages/admin/post/index.vue", "frontend/pages/admin/post/form.vue"} {
		page, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(page), `:flash="flash"`) {
			t.Errorf("%s does not pass flash to AdminLayout", rel)
		}
	}
}

func TestAdmin_RefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("first Admin: %v", err)
	}
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err == nil {
		t.Fatal("expected overwrite error, got nil")
	}
}

func TestAdmin_IndexUsesTableAndOverlaysStartClosed(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "frontend/pages/admin/post/index.vue"))
	if err != nil {
		t.Fatalf("read index.vue: %v", err)
	}
	got := string(data)
	for _, want := range []string{
		"@tanstack/vue-table",
		"@/components/ui/table",
		"useVueTable",
		`const pending = ref<Post | null>(null)`, // overlay starts closed
		"No post yet.",
		`:action="` + "`" + `${basePath}/${pending?.id}/delete` + "`" + `" method="post"`,
		"`${basePath}/${row.original.id}/edit`",
		"{{ item.value }}",
		"@update:page",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("index.vue missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `:open="true"`) {
		t.Error("an overlay is server-rendered open; SSR does not emit teleported content")
	}
	if strings.Contains(got, "[[") || strings.Contains(got, "]]") {
		t.Errorf("generated output still contains Go template delimiters:\n%s", got)
	}
}
