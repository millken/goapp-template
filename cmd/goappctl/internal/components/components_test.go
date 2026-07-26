package components

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestClosure(t *testing.T) {
	cases := []struct {
		name         string
		in           []string
		wantResolved []string
		wantAdded    []string
	}{
		{"empty", nil, nil, nil},
		{"independent", []string{"db"}, []string{"db"}, nil},
		{"admin pulls session and db", []string{"admin"}, []string{"db", "session", "admin"}, []string{"db", "session"}},
		{"admin with db already picked", []string{"db", "admin"}, []string{"db", "session", "admin"}, []string{"session"}},
		{"all", []string{"ssr", "admin", "session", "db"}, []string{"db", "session", "admin", "ssr"}, nil},
		{"blank entries ignored", []string{"db", "", " "}, []string{"db"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resolved, added, err := Closure(c.in)
			if err != nil {
				t.Fatalf("Closure(%v): %v", c.in, err)
			}
			if !slices.Equal(resolved, c.wantResolved) {
				t.Errorf("resolved = %v, want %v", resolved, c.wantResolved)
			}
			if !slices.Equal(added, c.wantAdded) {
				t.Errorf("added = %v, want %v", added, c.wantAdded)
			}
		})
	}
}

// TestClosure_ResolvedIsInChecklistOrder pins that resolved is dependency-safe
// to iterate: db and session precede admin, which needs both.
func TestClosure_ResolvedIsInChecklistOrder(t *testing.T) {
	resolved, _, err := Closure([]string{"admin"})
	if err != nil {
		t.Fatalf("Closure: %v", err)
	}
	adminAt := slices.Index(resolved, "admin")
	for _, dep := range []string{"db", "session"} {
		if at := slices.Index(resolved, dep); at < 0 || at > adminAt {
			t.Errorf("%s at %d must precede admin at %d in %v", dep, at, adminAt, resolved)
		}
	}
}

func TestClosure_Errors(t *testing.T) {
	if _, _, err := Closure([]string{"redis"}); err == nil {
		t.Error("expected an error for an unknown component")
	}
	if _, _, err := Closure([]string{Tooling}); err == nil {
		t.Errorf("expected %q to be rejected as reserved", Tooling)
	}
}

func TestOff_AlwaysIncludesTooling(t *testing.T) {
	off := Off(Names())
	if !off[Tooling] {
		t.Errorf("Off must always strip %q, got %v", Tooling, off)
	}
	for _, n := range Names() {
		if off[n] {
			t.Errorf("%q is selected but marked off", n)
		}
	}

	off = Off(nil)
	for _, n := range append(Names(), Tooling) {
		if !off[n] {
			t.Errorf("%q should be off when nothing is selected", n)
		}
	}
}

// TestOwnedPathsExist is the drift guard: a renamed directory would silently
// turn a deletion into a no-op, leaving a stripped component's code behind.
// Paths are resolved against the repo root, four levels up from this package.
func TestOwnedPathsExist(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repo root not found at %s: %v", root, err)
	}
	for _, c := range All {
		for _, p := range c.Owned {
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				t.Errorf("component %q owns %q, which does not exist: %v", c.Name, p, err)
			}
		}
	}
	// Tooling paths that must exist in the template (the rest are optional
	// build artifacts, workspace files, or CI config).
	for _, p := range []string{
		"cmd/goappctl", "docs", ".github/workflows/goappctl.yml",
		"frontend/pages/admin/ssrfixture", "server/ssr_fixture_test.go",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("tooling path %q does not exist: %v", p, err)
		}
	}
}

func TestAdminOwnsCopiedUIComponents(t *testing.T) {
	c, ok := Get("admin")
	if !ok {
		t.Fatal("admin component missing")
	}
	for _, want := range []string{"frontend/src/components/ui", "frontend/src/lib"} {
		if !slices.Contains(c.Owned, want) {
			t.Errorf("admin.Owned missing %q; a trimmed project would ship the shadcn source", want)
		}
	}
}
