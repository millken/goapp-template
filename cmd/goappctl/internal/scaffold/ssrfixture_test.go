package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath is the committed rendering of templates/admin/index.vue.tmpl. It
// exists because that template is never itself a .vue file — it carries [[ ]]
// delimiters — so nothing in frontend/pages/ would otherwise exercise the
// components it emits. server/ssr_fixture_test.go renders it through QuickJS.
const fixturePath = "frontend/pages/admin/ssrfixture/index.vue"

// TestAdminIndexFixtureIsCurrent keeps the fixture honest. Without it, editing
// the template would leave the fixture — and therefore the only SSR coverage of
// a generated list page — testing a version of the page that no longer ships.
func TestAdminIndexFixtureIsCurrent(t *testing.T) {
	root := moduleRoot(t)

	committed, err := os.ReadFile(filepath.Join(root, fixturePath))
	if err != nil {
		t.Fatalf("read committed fixture: %v", err)
	}

	tmp := t.TempDir()
	if err := Admin("ssrfixture", Options{ModuleRoot: tmp, Module: testModule}); err != nil {
		t.Fatalf("Admin: %v", err)
	}
	fresh, err := os.ReadFile(filepath.Join(tmp, fixturePath))
	if err != nil {
		t.Fatalf("read generated fixture: %v", err)
	}

	// The committed copy carries a leading explanatory comment the generator does
	// not emit; everything from <script setup onward must match byte for byte.
	const marker = "<script setup"
	i := strings.Index(string(committed), marker)
	if i < 0 {
		t.Fatalf("committed fixture has no %q — was it replaced by hand?", marker)
	}

	if string(committed[i:]) != string(fresh) {
		t.Errorf("%s is stale: the template has changed since it was generated.\n"+
			"Regenerate it — the steps are in the comment at the top of that file.", fixturePath)
	}
}
