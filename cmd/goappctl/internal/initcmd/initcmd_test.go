package initcmd

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is four levels up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repo root not found at %s", root)
	}
	return root
}

// copyTemplate copies the repo's git-tracked files (including uncommitted
// modifications) into a temp dir. Tracked-only is the point: it reproduces what
// a user's clone contains, so gitignored files — go.work above all — cannot leak
// in and mask a broken build.
func copyTemplate(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	dst := t.TempDir()

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "" {
			continue
		}
		src := filepath.Join(root, rel)
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "go.work")); err == nil {
		t.Fatal("go.work leaked into the copy; it would hide an unbuildable project")
	}
	return dst
}

func fingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		sum := sha256.Sum256(data)
		out[rel] = string(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// TestRun_DryRunWritesNothing is the guard for the flag people reach for exactly
// when they are unsure — it must not touch the tree.
func TestRun_DryRunWritesNothing(t *testing.T) {
	root := copyTemplate(t)
	before := fingerprint(t, root)

	var log bytes.Buffer
	err := Run(Options{
		Root: root, Module: "github.com/me/myapp", With: []string{"db"},
		DryRun: true, Force: true, Out: &log,
	})
	if err != nil {
		t.Fatalf("Run(dry-run): %v\n%s", err, log.String())
	}

	after := fingerprint(t, root)
	if len(before) != len(after) {
		t.Errorf("file count changed: %d -> %d", len(before), len(after))
	}
	for rel, sum := range before {
		if after[rel] != sum {
			t.Errorf("%s was modified during a dry run", rel)
		}
	}
	// It must still report a real plan, not stay silent.
	for _, want := range []string{"delete internal/service/session", "strip ", "module path:"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, log.String())
		}
	}
}

func TestRun_RefusesNonTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/me/already\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(Options{Root: root, Module: "github.com/me/x", Force: true, Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "not a pristine template clone") {
		t.Fatalf("expected a pristine-clone refusal, got %v", err)
	}
}

func TestRun_RefusesMissingGoMod(t *testing.T) {
	err := Run(Options{Root: t.TempDir(), Module: "github.com/me/x", Force: true, Out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Fatalf("expected a missing-go.mod refusal, got %v", err)
	}
}

// TestRun_Combos is §9's correctness check: init must produce a project that
// builds, vets and tests clean, for each combination that exercises a different
// part of the marker set. Run.step9 does the verification, so a nil error here
// means the transformed project passed all three.
//
// Gated because it runs `go build/vet/test` four times; .github/workflows/
// goappctl.yml sets GOAPPCTL_E2E=1.
func TestRun_Combos(t *testing.T) {
	if os.Getenv("GOAPPCTL_E2E") == "" {
		t.Skip("set GOAPPCTL_E2E=1 to run the full init matrix (slow: 4x build+vet+test)")
	}
	cases := []struct {
		name string
		with []string
		// wantAbsent are go.mod dependencies that must disappear with their
		// component — both are cgo, so a leak means a toolchain requirement the
		// user did not ask for.
		wantAbsent  []string
		wantPresent []string
	}{
		{
			name: "all-on", with: []string{"db", "session", "admin", "ssr"},
			wantPresent: []string{"mattn/go-sqlite3", "buke/quickjs-go"},
		},
		{
			name: "minimal", with: nil,
			wantAbsent: []string{"mattn/go-sqlite3", "buke/quickjs-go"},
		},
		{
			name: "no-ssr", with: []string{"db", "session", "admin"},
			wantAbsent:  []string{"buke/quickjs-go"},
			wantPresent: []string{"mattn/go-sqlite3"},
		},
		{
			// ssr's wiring is the most scattered (four Go files, two build
			// tags), and this is the only combo that strips db+session while
			// keeping it.
			name: "ssr-only", with: []string{"ssr"},
			wantAbsent:  []string{"mattn/go-sqlite3"},
			wantPresent: []string{"buke/quickjs-go"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := copyTemplate(t)
			var log bytes.Buffer
			err := Run(Options{
				Root: root, Module: "github.com/me/demo", With: c.with,
				Force: true, Out: &log,
			})
			if err != nil {
				t.Fatalf("Run: %v\n%s", err, log.String())
			}

			gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil {
				t.Fatalf("read go.mod: %v", err)
			}
			for _, dep := range c.wantAbsent {
				if bytes.Contains(gomod, []byte(dep)) {
					t.Errorf("go.mod still requires %s", dep)
				}
			}
			for _, dep := range c.wantPresent {
				if !bytes.Contains(gomod, []byte(dep)) {
					t.Errorf("go.mod lost %s", dep)
				}
			}
			// §11's self-healing claim: cmd/goappctl was the only consumer of
			// x/tools, so tidy must drop it from the generated project.
			if bytes.Contains(gomod, []byte("golang.org/x/tools")) {
				t.Error("go.mod still requires golang.org/x/tools after the tool was removed")
			}
			// No marker may survive anywhere.
			assertNoMarkers(t, root)
			// The tool must not ship itself.
			for _, rel := range []string{"cmd/goappctl", "internal/scaffold", "commands/gen.go", "docs", "go.work"} {
				if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
					t.Errorf("%s survived init", rel)
				}
			}
			// config.yaml must exist, or the project cannot serve.
			if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
				t.Errorf("config.yaml was not created: %v", err)
			}
		})
	}
}

func assertNoMarkers(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("goappctl:")) {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s still contains a goappctl marker", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
