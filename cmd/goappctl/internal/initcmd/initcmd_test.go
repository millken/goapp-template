package initcmd

import (
	"bytes"
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/millken/goapp-template/cmd/goappctl/internal/components"
	"github.com/millken/goapp-template/cmd/goappctl/internal/markers"
	"github.com/millken/goapp-template/cmd/goappctl/internal/scaffold"
	"sort"
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
			name: "all-on", with: []string{"db", "session", "admin", "storage", "ssr"},
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
		{
			// The combination the marker layout exists for: the admin area
			// present, its file manager gone. A leak here is a reference to a
			// deleted package, so this fails at build rather than subtly.
			name: "admin-without-storage", with: []string{"db", "session", "admin"},
			wantPresent: []string{"mattn/go-sqlite3"},
		},
		{
			// Storage with no admin: the service and the public route survive
			// with no UI at all.
			name: "storage-only", with: []string{"storage"},
			wantAbsent: []string{"mattn/go-sqlite3", "buke/quickjs-go"},
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

// TestRun_ThenGen is the handoff between the two commands: a project that init
// produced must be one that gen can extend, and the result must still compile.
// It also covers the auto-mount edit against a real (stripped) mount_gen.go
// rather than the fixture used by the scaffold unit tests.
func TestRun_ThenGen(t *testing.T) {
	if os.Getenv("GOAPPCTL_E2E") == "" {
		t.Skip("set GOAPPCTL_E2E=1 to run init+gen end to end (slow)")
	}
	root := copyTemplate(t)
	var log bytes.Buffer
	if err := Run(Options{
		Root: root, Module: "github.com/me/demo", With: []string{"admin"},
		Force: true, Out: &log,
	}); err != nil {
		t.Fatalf("init: %v\n%s", err, log.String())
	}

	p, err := scaffold.Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if p.Module != "github.com/me/demo" {
		t.Errorf("Module = %q, want the rewritten path", p.Module)
	}
	if !p.HasDB || !p.HasAdmin {
		t.Fatalf("expected db+admin present after --with admin closure, got %+v", p)
	}

	if err := scaffold.Resource("post", scaffold.Options{ModuleRoot: root, Module: p.Module}); err != nil {
		t.Fatalf("gen resource: %v", err)
	}
	added, err := scaffold.AddMount(root, p.Module, "post")
	if err != nil {
		t.Fatalf("AddMount: %v", err)
	}
	if !added {
		t.Error("AddMount reported no change on a fresh resource")
	}
	if err := scaffold.Admin("post", scaffold.Options{ModuleRoot: root, Module: p.Module}); err != nil {
		t.Fatalf("gen admin: %v", err)
	}

	mount, err := os.ReadFile(filepath.Join(root, scaffold.MountGenPath))
	if err != nil {
		t.Fatalf("read mount_gen.go: %v", err)
	}
	if !bytes.Contains(mount, []byte("post.Mount(eng, svc)")) {
		t.Errorf("mount region missing the new resource:\n%s", mount)
	}

	// The generated code must compile against the trimmed project, including
	// the admin resource, which is generated but mounted by hand.
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build after gen:\n%s", out)
	}
}

// assertNoMarkers re-parses every file with markers.Strip rather than doing a
// blunt substring search: several test files document the marker mechanism in
// prose (e.g. "guarded by its own goappctl:storage marker, not a nil check"),
// and a bytes.Contains check cannot tell that comment apart from a real
// surviving `//goappctl:name` block. Strip can, because it requires the exact
// comment-syntax prefix a real marker uses; prose fails that match and the
// file strips to itself unchanged.
func assertNoMarkers(t *testing.T, root string) {
	t.Helper()
	opts := markers.Options{Known: components.Known()}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !markers.HasMarkers(data) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if !markers.Supported(p) {
			// No comment form to have stripped it with, so any mention here
			// would have shipped verbatim into every generated project.
			t.Errorf("%s still contains a goappctl marker (unsupported file type)", rel)
			return nil
		}
		out, _, err := markers.Strip(rel, data, opts)
		if err != nil {
			t.Errorf("%s: still contains a malformed goappctl marker: %v", rel, err)
			return nil
		}
		if !bytes.Equal(out, data) {
			t.Errorf("%s still contains a goappctl marker", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// A stray marker string in a dot-directory used to make init refuse to run: the
// marker pass errors on a marker in a file type it has no comment form for, and
// the walk descended into scratch and editor directories. Real case: this
// project's own .superpowers scratch notes contain diffs full of marker lines.
func TestWalkFiles_SkipsDotDirectories(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"main.go",
		".superpowers/sdd/review.diff", // marker text, unsupported extension
		".vscode/settings.json",
		"node_modules/pkg/index.js",
		"internal/keep.go",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("goappctl:admin\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var seen []string
	if err := walkFiles(root, func(p string, d fs.DirEntry) error {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		seen = append(seen, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatalf("walkFiles: %v", err)
	}

	sort.Strings(seen)
	want := []string{"internal/keep.go", "main.go"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("visited %v, want %v", seen, want)
	}
}
