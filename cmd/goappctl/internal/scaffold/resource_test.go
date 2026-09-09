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
		{"admin", Admin, "internal/controller/admin/post/handler.go"},
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

// TestGeneratedWriteHandlersValidate covers both generators, because a write
// handler that redirects without validating is the gap this exists to close. The
// re-render must pass `item` back — that is what repopulates the inputs, and it
// is why there is no `old` prop.
func TestGeneratedWriteHandlersValidate(t *testing.T) {
	for _, c := range []struct {
		name      string
		gen       func(string, Options) error
		handler   string
		form      string
		formWants []string
	}{
		{
			"resource", Resource,
			"internal/controller/post/handler.go", "frontend/pages/post/form.vue",
			[]string{`errors?: Record<string, string>`, `errors?.name`, `:value="item.name"`},
		},
		{
			"admin", Admin,
			"internal/controller/admin/post/handler.go", "frontend/pages/admin/post/form.vue",
			[]string{`errors?: Record<string, string>`, `errors?.name`, `:model-value="item.name"`},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := c.gen("post", Options{ModuleRoot: root, Module: testModule}); err != nil {
				t.Fatalf("generate: %v", err)
			}

			handler, err := os.ReadFile(filepath.Join(root, c.handler))
			if err != nil {
				t.Fatalf("read %s: %v", c.handler, err)
			}
			for _, want := range []string{
				`"` + testModule + `/internal/validate"`,
				`validate.Required`,
				// Create passes 0; Update passes the row's own id so the
				// uniqueness rule skips it.
				`ct.validateItem(ctx, item, 0)`,
				`ct.validateItem(ctx, item, id)`,
				`ct.renderForm(c, item, v.Errors())`,
			} {
				if !strings.Contains(string(handler), want) {
					t.Errorf("%s missing %q:\n%s", c.handler, want, handler)
				}
			}

			form, err := os.ReadFile(filepath.Join(root, c.form))
			if err != nil {
				t.Fatalf("read %s: %v", c.form, err)
			}
			for _, want := range c.formWants {
				if !strings.Contains(string(form), want) {
					t.Errorf("%s missing %q:\n%s", c.form, want, form)
				}
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

// The CSRF check is global — it runs on every unsafe method, in the session
// middleware, not per area. A generated public resource's forms therefore need a
// token exactly as the admin's do, and the field cannot come from the admin's
// CsrfField component because a build without the admin component has no such
// file. Without both halves every create, edit and delete in a freshly generated
// resource answers 403.
func TestResource_FormsCarryTheCSRFField(t *testing.T) {
	root := t.TempDir()
	if err := Resource("widget", Options{ModuleRoot: root, Module: testModule}); err != nil {
		t.Fatalf("Resource: %v", err)
	}

	for _, rel := range []string{"frontend/pages/widget/index.vue", "frontend/pages/widget/form.vue"} {
		page, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		got := string(page)
		if !strings.Contains(got, `name="_csrf"`) {
			t.Errorf("%s posts without a CSRF field", rel)
		}
		if !strings.Contains(got, "csrfToken?: string") {
			t.Errorf("%s does not declare the csrfToken prop, so the field renders empty", rel)
		}
		// The import path, not the word: "CsrfField" also appears in the prop's
		// own comment explaining why it is not used, so matching the bare name
		// finds the explanation rather than the mistake.
		if strings.Contains(got, "components/admin/") {
			t.Errorf("%s imports from components/admin, which a no-admin build deletes", rel)
		}
	}

	handler, err := os.ReadFile(filepath.Join(root, "internal/controller/widget/handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	h := string(handler)
	if !strings.Contains(h, `c.Set("csrfToken"`) {
		t.Error("handler.go never injects the token, so the field renders empty")
	}
	if !strings.Contains(h, "ct.Session == nil") {
		t.Error("handler.go does not tolerate a build without the session component")
	}
}
