// Package initcmd implements `goappctl init`: the in-place transform that turns
// a clone of the template into a project.
//
// The pipeline is ordered and each step is a plain function, so the order is
// readable at the top of Run. Two orderings are load-bearing:
//
//   - tooling removal precedes `go mod tidy`, so tidy drops the tool's own
//     dependencies (x/tools, cobra's generator use) from the generated go.mod;
//   - marker stripping precedes goimports, which is what removes the ordinary
//     imports the stripped code used to need.
package initcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/imports"

	"github.com/millken/goapp-template/cmd/goappctl/internal/components"
	"github.com/millken/goapp-template/cmd/goappctl/internal/markers"
)

const (
	// TemplateModule is the module path a pristine clone must have. It doubles
	// as the idempotency guard: after init the path differs, so a second run
	// refuses instead of half-transforming an already-transformed tree.
	TemplateModule = "github.com/millken/goapp-template"
	// templateAppName is the placeholder binary/app name in the template.
	templateAppName = "myapp"
)

// skipDirs are never walked: VCS metadata and build output.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "dist": true, "bin": true}

// walkFiles visits every file under root, pruning skipDirs. The passes that
// rewrite the tree (markers, identity, goimports) all want exactly this.
func walkFiles(root string, visit func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		return visit(p, d)
	})
}

// Options configures a run.
type Options struct {
	Root      string   // directory to transform (the cwd)
	Module    string   // new module path
	Name      string   // new app name; defaults to the module's last segment
	With      []string // selected components
	DryRun    bool
	Force     bool // skip the clean-worktree check
	GitReinit bool // rm -rf .git && git init
	Out       io.Writer
}

// Run executes the pipeline.
func Run(o Options) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Root == "" {
		o.Root = "."
	}
	if o.Module == "" {
		return errors.New("--module is required")
	}
	if o.Name == "" {
		o.Name = lastSegment(o.Module)
	}
	if err := validateName(o.Name); err != nil {
		return err
	}

	// step 1: guardrails.
	if err := checkGuardrails(o); err != nil {
		return err
	}

	// step 2: selection + dependency closure.
	resolved, added, err := components.Closure(o.With)
	if err != nil {
		return err
	}
	if len(added) > 0 {
		fmt.Fprintf(o.Out, "adding %s (required by your selection)\n", strings.Join(added, ", "))
	}
	off := components.Off(resolved)
	shown := resolved
	if len(shown) == 0 {
		shown = []string{"(none)"}
	}
	fmt.Fprintf(o.Out, "components: %s\n", strings.Join(shown, ", "))
	if o.DryRun {
		fmt.Fprintln(o.Out, "\n-- dry run: nothing will be written --")
	}

	// step 3 + 7: delete owned paths, then template-only paths. Both are
	// deletions, so they share one reporting pass; tooling must go before tidy.
	var toDelete []string
	for _, c := range components.All {
		if off[c.Name] {
			toDelete = append(toDelete, c.Owned...)
		}
	}
	toDelete = append(toDelete, components.ToolingPaths...)
	if err := deletePaths(o, toDelete); err != nil {
		return err
	}

	// step 4: strip marker blocks.
	if err := stripMarkers(o, off); err != nil {
		return err
	}

	// step 4a: package.json scripts, only when ssr is off.
	if off["ssr"] {
		if err := stripSSRScripts(o); err != nil {
			return err
		}
	}

	// step 5: materialize config.yaml from the (now stripped) sample.
	if err := materializeConfig(o); err != nil {
		return err
	}

	// step 6: rewrite identity.
	if err := rewriteIdentity(o); err != nil {
		return err
	}

	if o.DryRun {
		fmt.Fprintln(o.Out, "\ndry run complete; re-run without --dry-run to apply")
		return nil
	}

	// step 8: goimports, then tidy.
	if err := formatGoFiles(o); err != nil {
		return err
	}
	fmt.Fprintln(o.Out, "running go mod tidy")
	if err := runCmd(o, "go", "mod", "tidy"); err != nil {
		return err
	}

	// step 9: verify, then optionally re-init git.
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"test", "./..."}} {
		label := "go " + strings.Join(args, " ")
		fmt.Fprintf(o.Out, "verifying: %s\n", label)
		if err := runCmd(o, "go", args...); err != nil {
			return fmt.Errorf("%s failed on the transformed project: %w", label, err)
		}
	}
	if o.GitReinit {
		if err := gitReinit(o); err != nil {
			return err
		}
	}

	fmt.Fprintf(o.Out, "\ndone: %s is ready\n", o.Module)
	return nil
}

// checkGuardrails implements §3.1: init is destructive and in-place, so refuse
// to run on anything that is not a pristine template clone.
func checkGuardrails(o Options) error {
	data, err := os.ReadFile(filepath.Join(o.Root, "go.mod"))
	if err != nil {
		return fmt.Errorf("no go.mod in %s — run init inside a clone of the template: %w", o.Root, err)
	}
	got := modulePath(data)
	if got != TemplateModule {
		return fmt.Errorf("module path is %q, want %q — this directory is not a pristine template clone "+
			"(already initialized?)", got, TemplateModule)
	}
	if o.Force || o.DryRun {
		return nil
	}
	out, err := exec.Command("git", "-C", o.Root, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("not a git repository (pass --force to transform anyway): %w", err)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return fmt.Errorf("git worktree is not clean; commit, stash, or pass --force:\n%s", out)
	}
	return nil
}

var modulePathRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)

func modulePath(goMod []byte) string {
	if m := modulePathRe.FindSubmatch(goMod); m != nil {
		return string(m[1])
	}
	return ""
}

// validateName rejects names that cannot work as a binary name. The name also
// drives buildinfo.AppName and therefore the <NAME>_HOME env var.
func validateName(name string) error {
	if strings.ContainsAny(name, `/\ `) {
		return fmt.Errorf("invalid app name %q: no slashes or spaces", name)
	}
	return nil
}

// lastSegment returns the final path element of a module path, the default app
// name (e.g. "github.com/me/myapp" → "myapp").
func lastSegment(module string) string {
	parts := strings.Split(strings.TrimSuffix(module, "/"), "/")
	return parts[len(parts)-1]
}

func deletePaths(o Options, paths []string) error {
	slices.Sort(paths)
	for _, rel := range slices.Compact(paths) {
		full := filepath.Join(o.Root, rel)
		if _, err := os.Lstat(full); err != nil {
			continue // tolerated: the template may not have this path
		}
		fmt.Fprintf(o.Out, "  delete %s\n", rel)
		if o.DryRun {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return fmt.Errorf("delete %s: %w", rel, err)
		}
	}
	return nil
}

// stripMarkers walks the tree and strips blocks. Files whose type has no comment
// form are checked for stray markers rather than silently left alone — a marker
// shipped verbatim into a generated project is a bug we want to hear about now.
func stripMarkers(o Options, off map[string]bool) error {
	opts := markers.Options{Off: off, Known: components.Known()}
	total, files := 0, 0
	err := walkFiles(o.Root, func(p string, _ fs.DirEntry) error {
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !markers.HasMarkers(src) {
			return nil
		}
		rel, _ := filepath.Rel(o.Root, p)
		if !markers.Supported(p) {
			return fmt.Errorf("%s contains a goappctl marker but its file type has no comment form; "+
				"move the marker into a supported file (.go/.ts/.yaml/.md)", rel)
		}
		out, n, err := markers.Strip(rel, src, opts)
		if err != nil {
			return err
		}
		if n > 0 {
			fmt.Fprintf(o.Out, "  strip %s (%d block(s))\n", rel, n)
		}
		total, files = total+n, files+1
		if o.DryRun || bytes.Equal(out, src) {
			return nil
		}
		return os.WriteFile(p, out, 0o644)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "markers: %d block(s) stripped across %d file(s)\n", total, files)
	return nil
}

func stripSSRScripts(o Options) error {
	rel := "frontend/package.json"
	full := filepath.Join(o.Root, rel)
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out, err := markers.StripSSRScripts(src)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if bytes.Equal(out, src) {
		return nil
	}
	fmt.Fprintf(o.Out, "  edit %s (drop SSR scripts)\n", rel)
	if o.DryRun {
		return nil
	}
	return os.WriteFile(full, out, 0o644)
}

// materializeConfig copies the stripped sample to config.yaml. Without this a
// generated project has no config file at all: the template's config.yaml is
// gitignored, so a clone never contains one, and serve fails at Start with a
// missing config section.
func materializeConfig(o Options) error {
	src := filepath.Join(o.Root, "config.example.yaml")
	dst := filepath.Join(o.Root, "config.yaml")
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		fmt.Fprintln(o.Out, "  keep config.yaml (already present)")
		return nil
	}
	fmt.Fprintln(o.Out, "  create config.yaml (from config.example.yaml)")
	if o.DryRun {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// identityExts are the file types carrying the module path.
var identityExts = map[string]bool{".go": true, ".md": true, ".json": true, ".mod": true, ".yaml": true, ".yml": true}

// appNameFiles carry the app name / env prefix. buildinfo.go is the single
// source of truth at runtime (paths.go derives <NAME>_HOME from it), so the rest
// are documentation, build config, and editor config.
//
// commands/paths_test.go is deliberately absent: its "myapp"/"MYAPP_HOME"
// literals are a table-driven test of that derivation, not the app's identity.
var appNameFiles = []string{
	"internal/buildinfo/buildinfo.go",
	"commands/paths.go",
	"Makefile",
	"README.md",
	"frontend/package.json",
	".vscode/launch.json",
	".vscode/tasks.json",
}

func rewriteIdentity(o Options) error {
	// Module path: everywhere it can appear.
	changed := 0
	err := walkFiles(o.Root, func(p string, d fs.DirEntry) error {
		if !identityExts[filepath.Ext(p)] && d.Name() != "Makefile" {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Contains(src, []byte(TemplateModule)) {
			return nil
		}
		changed++
		if o.DryRun {
			return nil
		}
		out := bytes.ReplaceAll(src, []byte(TemplateModule), []byte(o.Module))
		return os.WriteFile(p, out, 0o644)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "module path: %s -> %s (%d file(s))\n", TemplateModule, o.Module, changed)

	// App name: a separate identity dimension from the module path, since the
	// binary name need not be the module's last segment.
	if o.Name == templateAppName {
		return nil
	}
	renamed := 0
	for _, rel := range appNameFiles {
		full := filepath.Join(o.Root, rel)
		src, err := os.ReadFile(full)
		if err != nil {
			continue // deleted with its component, or absent in this template
		}
		out := bytes.ReplaceAll(src, []byte(strings.ToUpper(templateAppName)), []byte(strings.ToUpper(o.Name)))
		out = bytes.ReplaceAll(out, []byte(templateAppName), []byte(o.Name))
		if bytes.Equal(out, src) {
			continue
		}
		renamed++
		if o.DryRun {
			continue
		}
		if err := os.WriteFile(full, out, 0o644); err != nil {
			return fmt.Errorf("rewrite %s: %w", rel, err)
		}
	}
	fmt.Fprintf(o.Out, "app name: %s -> %s (%d file(s))\n", templateAppName, o.Name, renamed)
	return nil
}

// formatGoFiles runs goimports over every Go file. It is used as a library
// rather than a binary because users are not expected to have goimports
// installed; `go mod tidy` in step 8 then drops x/tools from the generated
// go.mod, since cmd/goappctl (its only consumer) was deleted in step 7.
func formatGoFiles(o Options) error {
	n := 0
	err := walkFiles(o.Root, func(p string, _ fs.DirEntry) error {
		if filepath.Ext(p) != ".go" {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := imports.Process(p, src, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
		if err != nil {
			rel, _ := filepath.Rel(o.Root, p)
			return fmt.Errorf("goimports %s: %w", rel, err)
		}
		if bytes.Equal(out, src) {
			return nil
		}
		n++
		return os.WriteFile(p, out, 0o644)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "goimports: %d file(s) rewritten\n", n)
	return nil
}

func runCmd(o Options, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = o.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s:\n%s", name, strings.Join(args, " "), out)
	}
	return nil
}

func gitReinit(o Options) error {
	fmt.Fprintln(o.Out, "re-initializing git history")
	if err := os.RemoveAll(filepath.Join(o.Root, ".git")); err != nil {
		return err
	}
	return runCmd(o, "git", "init")
}
