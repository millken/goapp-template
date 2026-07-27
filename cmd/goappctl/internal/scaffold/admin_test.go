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
		`ct.flash(c, "success", "Post 已创建")`,
		`ct.flash(c, "success", "Post 已更新")`,
		`ct.flash(c, "success", "Post 已删除")`,
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
			t.Errorf("%s does not pass flash to AdminShell", rel)
		}
	}
}

// TestAdmin_GeneratedFormsCarryTheCSRFField covers the one place nothing else
// looks. The frontend audit reads frontend/pages and the admin composites, never
// the templates; index.vue.tmpl is caught indirectly because the committed SSR
// fixture drifts, but form.vue.tmpl has no fixture — the regeneration recipe
// deletes it. So a change that dropped the field from every create and edit page
// the generator produces would go unnoticed until enforcement 403s them all,
// which is exactly the failure the field exists to prevent.
func TestAdmin_GeneratedFormsCarryTheCSRFField(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}

	for _, rel := range []string{"frontend/pages/admin/post/index.vue", "frontend/pages/admin/post/form.vue"} {
		page, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		got := string(page)
		// The prop has to reach the page, and the page has to hand it to the
		// composites that own the forms. Either half missing renders an empty
		// field, which looks fine and fails on submit.
		for _, want := range []string{"csrfToken", `:csrf-token="csrfToken"`} {
			if !strings.Contains(got, want) {
				t.Errorf("%s is missing %q", rel, want)
			}
		}
	}

	// The form page's own submit goes through CsrfField directly rather than a
	// composite, so it needs the element — not merely the import, which is what
	// a bare "CsrfField" check would match while the field itself was gone.
	form, err := os.ReadFile(filepath.Join(root, "frontend/pages/admin/post/form.vue"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(form), "<CsrfField") {
		t.Error("form.vue imports CsrfField but never renders it, so its form posts without a token")
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
		`import DataTable from '@/components/admin/DataTable.vue'`,
		`import AdminShell from '@/components/admin/AdminShell.vue'`,
		`import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'`,
		`const pending = ref<Post | null>(null)`, // overlay starts closed
		"还没有 post。",
		"`${basePath}/${pending?.id}/delete`",
		"`${basePath}/${row.id}/edit`",
		`search-key="name"`,
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

func TestAdmin_RoutesGoThroughTheRegistrar(t *testing.T) {
	root := t.TempDir()
	if err := Admin("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "internal/controller/adminpost/handler.go"))
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		`r := adm.Resource(eng, "post")`,
		"r.GET(ct.base, ct.Index)",
		"r.POST(ct.base+\"/:id/delete\", ct.Delete)",
		`r.Menu("内容", "Post", ct.base)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("handler.go missing %q:\n%s", want, got)
		}
	}
	// A route registered outside the registrar has no permission check, which is
	// the hole this design exists to close.
	for _, gone := range []string{"adm.AuthMiddleware()", "eng.GET(", "eng.POST(", "adm.AddMenuItem("} {
		if strings.Contains(got, gone) {
			t.Errorf("handler.go still contains %q — routes must go through the registrar:\n%s", gone, got)
		}
	}
}
