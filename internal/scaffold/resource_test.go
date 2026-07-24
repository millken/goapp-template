package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResource_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	if err := Resource("blog-post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("Resource: %v", err)
	}

	want := []string{
		"internal/module/blogpost/handler.go",
		"internal/module/blogpost/model.go",
		"frontend/pages/blog-post/index.vue",
		"frontend/pages/blog-post/form.vue",
	}
	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("expected file %s: %v", rel, err)
		}
	}
}

func TestResource_RefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := Resource("post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("first Resource: %v", err)
	}
	err := Resource("post", Options{ModuleRoot: root})
	if err == nil || !strings.Contains(err.Error(), "use --force") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
}

func TestResource_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	if err := Resource("post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("first Resource: %v", err)
	}
	if err := Resource("post", Options{ModuleRoot: root, Force: true}); err != nil {
		t.Fatalf("force Resource: %v", err)
	}
}

func TestResource_TemplateContent(t *testing.T) {
	root := t.TempDir()
	if err := Resource("post", Options{ModuleRoot: root}); err != nil {
		t.Fatalf("Resource: %v", err)
	}
	model, err := os.ReadFile(filepath.Join(root, "internal/module/post/model.go"))
	if err != nil {
		t.Fatalf("read model.go: %v", err)
	}
	if !strings.Contains(string(model), "type Post struct") {
		t.Errorf("model.go missing 'type Post struct':\n%s", model)
	}
	if !strings.Contains(string(model), `"post"`) {
		t.Errorf("model.go missing table name 'post':\n%s", model)
	}
}
