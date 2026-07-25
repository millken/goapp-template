package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/me/demo\n\ngo 1.26.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name      string
		dirs      []string
		wantDB    bool
		wantAdmin bool
	}{
		{"minimal", []string{"internal/controller"}, false, false},
		{"with db", []string{"internal/controller", "internal/service/db"}, true, false},
		{"with admin", []string{"internal/controller", "internal/controller/admin", "internal/service/db"}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Detect(writeProject(t, c.dirs...))
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if p.Module != "github.com/me/demo" {
				t.Errorf("Module = %q", p.Module)
			}
			if p.HasDB != c.wantDB {
				t.Errorf("HasDB = %v, want %v", p.HasDB, c.wantDB)
			}
			if p.HasAdmin != c.wantAdmin {
				t.Errorf("HasAdmin = %v, want %v", p.HasAdmin, c.wantAdmin)
			}
		})
	}
}

func TestDetect_Errors(t *testing.T) {
	if _, err := Detect(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("expected a missing-go.mod error, got %v", err)
	}
	// go.mod but no controller tree: not a goapp-template project.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "internal/controller") {
		t.Errorf("expected an internal/controller error, got %v", err)
	}
}
