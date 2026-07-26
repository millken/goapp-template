package scaffold

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registryStub serves a fixed set of registry items on loopback, so these tests
// exercise the real HTTP path without leaving the machine.
func registryStub(t *testing.T, items map[string]registryItem) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".json")
		item, ok := items[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestResolveItems_PullsRegistryDependencies(t *testing.T) {
	base := registryStub(t, map[string]registryItem{
		"pagination": {
			Name:                 "pagination",
			Dependencies:         []string{"reka-ui"},
			RegistryDependencies: []string{"button"},
			Files:                []registryFile{{Path: "ui/pagination/Pagination.vue", Content: "<template />"}},
		},
		"button": {
			Name:                 "button",
			Dependencies:         []string{"reka-ui", "class-variance-authority"},
			RegistryDependencies: []string{"utils"},
			Files:                []registryFile{{Path: "ui/button/Button.vue", Content: "<template />"}},
		},
		"utils": {
			Name:  "utils",
			Files: []registryFile{{Path: "lib/utils.ts", Content: "export {}"}},
		},
	})

	got, err := resolveItems(base, []string{"pagination"})
	if err != nil {
		t.Fatalf("resolveItems: %v", err)
	}

	var names []string
	for _, it := range got {
		names = append(names, it.Name)
	}
	// Dependencies first: writing utils before button before pagination means a
	// partially-applied run never leaves a file importing something absent.
	want := []string{"utils", "button", "pagination"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestResolveItems_DeduplicatesSharedDependencies(t *testing.T) {
	base := registryStub(t, map[string]registryItem{
		"alert":  {Name: "alert", RegistryDependencies: []string{"utils"}},
		"badge":  {Name: "badge", RegistryDependencies: []string{"utils"}},
		"utils":  {Name: "utils"},
	})

	got, err := resolveItems(base, []string{"alert", "badge"})
	if err != nil {
		t.Fatalf("resolveItems: %v", err)
	}
	seen := map[string]int{}
	for _, it := range got {
		seen[it.Name]++
	}
	if seen["utils"] != 1 {
		t.Errorf("utils fetched %d times, want 1", seen["utils"])
	}
	if len(got) != 3 {
		t.Errorf("got %d items, want 3", len(got))
	}
}

func TestFetchItem_UnknownNameSaysSo(t *testing.T) {
	base := registryStub(t, map[string]registryItem{})

	_, err := fetchItem(base, "nosuchthing")
	if err == nil {
		t.Fatal("want an error for an unknown component")
	}
	// A wrong name and an unreachable registry are the two failures a developer
	// actually hits; the message must say which.
	if !strings.Contains(err.Error(), "nosuchthing") {
		t.Errorf("error should name the component, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no component named") {
		t.Errorf("error should identify this as an unknown name, got: %v", err)
	}
}

func TestFetchItem_UnreachableRegistrySaysSo(t *testing.T) {
	// A port nothing listens on: a transport failure, not a 404.
	_, err := fetchItem("http://127.0.0.1:1", "button")
	if err == nil {
		t.Fatal("want an error when the registry is unreachable")
	}
	if strings.Contains(err.Error(), "no component named") {
		t.Errorf("a transport failure must not be reported as an unknown name: %v", err)
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("error should mention the registry, got: %v", err)
	}
}

func TestUIDestPath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ui/button/Button.vue", "frontend/src/components/ui/button/Button.vue"},
		{"ui/button/index.ts", "frontend/src/components/ui/button/index.ts"},
		{"lib/utils.ts", "frontend/src/lib/utils.ts"},
		{"composables/useFoo.ts", "frontend/src/composables/useFoo.ts"},
	} {
		got, err := uiDestPath(c.in)
		if err != nil {
			t.Errorf("uiDestPath(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("uiDestPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Registry content is third-party JSON fetched over HTTP, and the base URL is
// overridable, so a path that escapes the project root must be refused rather
// than written.
func TestUIDestPath_RejectsPathsThatEscape(t *testing.T) {
	for _, bad := range []string{
		"ui/../../../etc/cron.d/x",
		"../outside.ts",
		"/etc/passwd",
		"ui/./button/Button.vue",
		"",
		`ui/foo\..\..\bar`, // backslash traversal: inert on unix, escapes on Windows
	} {
		if got, err := uiDestPath(bad); err == nil {
			t.Errorf("uiDestPath(%q) = %q, want an error", bad, got)
		}
	}
}

// The registry ships imports pointing at its own layout. The upstream CLI
// rewrites them from components.json; skipping this writes code that cannot
// build — Pagination*.vue imports buttonVariants that way.
func TestRewriteRegistryImports(t *testing.T) {
	in := `import { buttonVariants } from "@/registry/default/ui/button"
import { cn } from "@/lib/utils"
import { Primitive } from "reka-ui"`
	want := `import { buttonVariants } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { Primitive } from "reka-ui"`

	if got := rewriteRegistryImports(in); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteRegistryImports_LeavesUnrelatedPathsAlone(t *testing.T) {
	in := `import x from "@/components/ui/button"
const s = "registry/default/ui is not an import"`
	if got := rewriteRegistryImports(in); got != in {
		t.Errorf("rewrote something it should not have:\n%s", got)
	}
}

func twoItemStub(t *testing.T) string {
	t.Helper()
	return registryStub(t, map[string]registryItem{
		"button": {
			Name:                 "button",
			Dependencies:         []string{"reka-ui"},
			RegistryDependencies: []string{"utils"},
			Files: []registryFile{
				{Path: "ui/button/Button.vue", Content: "import { cn } from \"@/registry/default/ui/button\"\n"},
			},
		},
		"utils": {
			Name:  "utils",
			Files: []registryFile{{Path: "lib/utils.ts", Content: "export const cn = 1\n"}},
		},
	})
}

func TestUI_WritesFilesWithRewrittenImports(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer

	if err := UI([]string{"button"}, UIOptions{
		ModuleRoot: root, BaseURL: twoItemStub(t), Out: &out,
	}); err != nil {
		t.Fatalf("UI: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "frontend/src/components/ui/button/Button.vue"))
	if err != nil {
		t.Fatalf("read Button.vue: %v", err)
	}
	if strings.Contains(string(got), "@/registry") {
		t.Errorf("registry import survived:\n%s", got)
	}
	if !strings.Contains(string(got), "@/components/ui/button") {
		t.Errorf("import was not rewritten:\n%s", got)
	}
	// The transitive dependency must land too, or the file above imports nothing.
	if _, err := os.Stat(filepath.Join(root, "frontend/src/lib/utils.ts")); err != nil {
		t.Errorf("transitive dependency not written: %v", err)
	}
}

func TestUI_RefusesToOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	base := twoItemStub(t)
	var out bytes.Buffer

	if err := UI([]string{"button"}, UIOptions{ModuleRoot: root, BaseURL: base, Out: &out}); err != nil {
		t.Fatalf("first UI: %v", err)
	}
	err := UI([]string{"button"}, UIOptions{ModuleRoot: root, BaseURL: base, Out: &out})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want an overwrite refusal mentioning --force, got %v", err)
	}
	if err := UI([]string{"button"}, UIOptions{ModuleRoot: root, BaseURL: base, Force: true, Out: &out}); err != nil {
		t.Fatalf("UI with Force: %v", err)
	}
}

func TestUI_DryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer

	if err := UI([]string{"button"}, UIOptions{
		ModuleRoot: root, BaseURL: twoItemStub(t), DryRun: true, Out: &out,
	}); err != nil {
		t.Fatalf("UI: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "frontend/src/components")); err == nil {
		t.Error("dry run created files")
	}
	if !strings.Contains(out.String(), "frontend/src/components/ui/button/Button.vue") {
		t.Errorf("dry run should still print the plan, got:\n%s", out.String())
	}
}

// A failure partway through the requested set must not leave half a component
// on disk: everything is fetched before anything is written.
func TestUI_WritesNothingWhenAnyItemFails(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer

	err := UI([]string{"button", "nosuchthing"}, UIOptions{
		ModuleRoot: root, BaseURL: twoItemStub(t), Out: &out,
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(root, "frontend/src/components")); err == nil {
		t.Error("a failed run left files behind")
	}
}
