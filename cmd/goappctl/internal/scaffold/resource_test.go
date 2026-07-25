package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResource_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	if err := Resource("blog-post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Resource: %v", err)
	}

	want := []string{
		"internal/controller/blogpost/handler.go",
		"internal/controller/blogpost/model.go",
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
	if err := Resource("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("first Resource: %v", err)
	}
	err := Resource("post", Options{ModuleRoot: root, Module: testModule})
	if err == nil || !strings.Contains(err.Error(), "use --force") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
}

func TestResource_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	if err := Resource("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("first Resource: %v", err)
	}
	if err := Resource("post", Options{ModuleRoot: root, Module: testModule, Force: true}); err != nil {
		t.Fatalf("force Resource: %v", err)
	}
}

func TestResource_TemplateContent(t *testing.T) {
	root := t.TempDir()
	if err := Resource("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Resource: %v", err)
	}
	model, err := os.ReadFile(filepath.Join(root, "internal/controller/post/model.go"))
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

// TestGeneratedImportsTargetModule is the regression for generated code
// importing the *template's* module path. The handler templates used to hardcode
// github.com/millken/goapp-template, which compiled inside the template repo and
// failed in every project produced from it.
func TestGeneratedImportsTargetModule(t *testing.T) {
	const foreign = "github.com/other/app"
	for _, c := range []struct {
		name string
		gen  func(string, Options) error
		path string
	}{
		{"resource", Resource, "internal/controller/post/handler.go"},
		{"admin", Admin, "internal/controller/adminpost/handler.go"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := c.gen("post", Options{ModuleRoot: root, Module: foreign}); err != nil {
				t.Fatalf("generate: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, c.path))
			if err != nil {
				t.Fatalf("read %s: %v", c.path, err)
			}
			if !strings.Contains(string(data), `"`+foreign+`/internal/app"`) {
				t.Errorf("%s does not import %s:\n%s", c.path, foreign, data)
			}
			if strings.Contains(string(data), "goapp-template") {
				t.Errorf("%s leaks the template's module path:\n%s", c.path, data)
			}
		})
	}
}

// TestGenerateRequiresModule: an empty module would silently emit `"/internal/app"`.
func TestGenerateRequiresModule(t *testing.T) {
	for name, gen := range map[string]func(string, Options) error{"resource": Resource, "admin": Admin} {
		t.Run(name, func(t *testing.T) {
			err := gen("post", Options{ModuleRoot: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), "Module is required") {
				t.Fatalf("expected a required-module error, got %v", err)
			}
		})
	}
}
