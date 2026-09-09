package scaffold

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// DefaultRegistry is the shadcn-vue registry `gen ui` reads. Items are served as
// <base>/<name>.json.
const DefaultRegistry = "https://shadcn-vue.com/r/styles/default"

// registryFile is one file of a component, as the registry ships it. Path is
// registry-relative (e.g. "ui/button/Button.vue" or "lib/utils.ts").
type registryFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// registryItem is one registry entry. Dependencies are npm packages;
// RegistryDependencies are other registry items this one imports from.
type registryItem struct {
	Name                 string         `json:"name"`
	Dependencies         []string       `json:"dependencies"`
	RegistryDependencies []string       `json:"registryDependencies"`
	Files                []registryFile `json:"files"`
}

// registryClient bounds every fetch; without a timeout an unreachable registry
// hangs the command instead of failing it.
var registryClient = &http.Client{Timeout: 30 * time.Second}

// fetchItem reads one registry item. It distinguishes the two failures a
// developer actually hits — a name that does not exist, and a registry it cannot
// reach — because the fix differs.
func fetchItem(base, name string) (registryItem, error) {
	url := base + "/" + name + ".json"
	resp, err := registryClient.Get(url)
	if err != nil {
		return registryItem{}, fmt.Errorf("cannot reach the component registry at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return registryItem{}, fmt.Errorf("no component named %q in the registry (%s)", name, url)
	}
	if resp.StatusCode != http.StatusOK {
		return registryItem{}, fmt.Errorf("component registry returned %s for %s", resp.Status, url)
	}

	var item registryItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return registryItem{}, fmt.Errorf("registry sent unreadable JSON for %q: %w", name, err)
	}
	if item.Name == "" {
		item.Name = name
	}
	return item, nil
}

// resolveItems fetches every requested item plus its transitive
// registryDependencies, deduplicated. Dependencies come back before the items
// that need them, so a run interrupted midway never leaves a file importing
// something that was never written.
func resolveItems(base string, names []string) ([]registryItem, error) {
	seen := map[string]bool{}
	var out []registryItem

	var visit func(string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true

		item, err := fetchItem(base, name)
		if err != nil {
			return err
		}
		for _, dep := range item.RegistryDependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		out = append(out, item)
		return nil
	}

	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// registryImportPrefix is how the registry refers to its own components. The
// upstream CLI rewrites this from components.json; we have no such file, so the
// mapping is fixed here — and it is not optional: Pagination*.vue imports
// buttonVariants through it, so skipping the rewrite writes code that will not
// build.
const registryImportPrefix = "@/registry/default/ui"

// uiDestPath maps a registry-relative path to a project-relative one. Component
// files live under ui/, which belongs beneath frontend/src/components/;
// everything else (lib/utils.ts) is already relative to frontend/src.
//
// The path comes from a third party over HTTP, and this is where it turns into
// somewhere we write, so it is also where it gets checked: anything that is not
// already a plain relative path is refused rather than cleaned, because a
// registry emitting `..` is broken or hostile and either way should not be
// silently accommodated.
func uiDestPath(registryPath string) (string, error) {
	cleanPath := path.Clean(registryPath)
	// path.Clean only ever treats "/" as a separator, but the writer joins with
	// filepath.Join, which on Windows also honours "\" — so a backslash would
	// slip past a slash-only check and escape there. Registry paths are
	// forward-slash by convention, so refusing backslashes outright costs
	// nothing.
	if registryPath == "" ||
		strings.HasPrefix(registryPath, "/") ||
		strings.Contains(registryPath, `\`) ||
		strings.HasPrefix(cleanPath, "..") || cleanPath != registryPath {
		return "", fmt.Errorf("registry file path %q is not a plain relative path", registryPath)
	}
	// Only the two directories the registry actually ships (verified across 17
	// live items: 142 files under ui/, one under lib/). Without this, a path like
	// "main.ts" would map onto frontend/src/main.ts — the real Vite entry point —
	// and --force would overwrite it with component source.
	switch {
	case strings.HasPrefix(registryPath, "ui/"):
		return "frontend/src/components/" + registryPath, nil
	case strings.HasPrefix(registryPath, "lib/"):
		return "frontend/src/" + registryPath, nil
	}
	return "", fmt.Errorf("registry file path %q is outside ui/ and lib/, the only directories components ship into", registryPath)
}

// rewriteRegistryImports points registry-internal imports at where the files
// actually land.
func rewriteRegistryImports(content string) string {
	return strings.ReplaceAll(content, registryImportPrefix, "@/components/ui")
}

// missingDeps reports which npm packages the components need that
// frontend/package.json does not already list. A project without a package.json
// gets the full list rather than an error — it is a report, not a gate.
func missingDeps(moduleRoot string, deps []string) ([]string, error) {
	installed := map[string]bool{}

	data, err := os.ReadFile(filepath.Join(moduleRoot, "frontend/package.json"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read frontend/package.json: %w", err)
	}
	if err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil, fmt.Errorf("frontend/package.json is not valid JSON: %w", err)
		}
		for name := range pkg.Dependencies {
			installed[name] = true
		}
		for name := range pkg.DevDependencies {
			installed[name] = true
		}
	}

	var out []string
	for _, d := range deps {
		if !installed[d] && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out, nil
}

// UIOptions controls `gen ui`.
type UIOptions struct {
	// ModuleRoot is the project root; empty means paths are relative to the cwd.
	ModuleRoot string
	// Force overwrites existing files, which is how you take an upstream update.
	Force bool
	// DryRun prints the plan and writes nothing.
	DryRun bool
	// BaseURL overrides the registry root. Empty uses DefaultRegistry; tests
	// point it at an httptest server so the suite stays offline.
	BaseURL string
	// Out receives the progress and dependency report.
	Out io.Writer
}

// UI fetches components from the registry and writes their source into the
// project. Every item is resolved before anything is written, so a bad name or
// an unreachable registry leaves the working tree untouched.
func UI(names []string, opts UIOptions) error {
	if len(names) == 0 {
		return fmt.Errorf("gen ui: name at least one component")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultRegistry
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}

	items, err := resolveItems(base, names)
	if err != nil {
		return err
	}

	// Resolve every destination before writing anything, so a hostile path or an
	// existing file stops the run rather than leaving it half applied.
	type placement struct{ dest, content string }
	var plan []placement
	claimed := map[string]string{} // destination -> the component that wants it
	for _, item := range items {
		for _, f := range item.Files {
			dest, err := uiDestPath(f.Path)
			if err != nil {
				return fmt.Errorf("gen ui: component %q: %w", item.Name, err)
			}
			// Two components wanting the same file is a registry problem, and
			// silently letting the second win would hide it.
			if owner, dup := claimed[dest]; dup {
				return fmt.Errorf("gen ui: %q and %q both write %s", owner, item.Name, dest)
			}
			claimed[dest] = item.Name
			plan = append(plan, placement{dest: dest, content: f.Content})
		}
	}

	if !opts.Force && !opts.DryRun {
		for _, p := range plan {
			full := filepath.Join(opts.ModuleRoot, p.dest)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("file exists (use --force to overwrite): %s", full)
			}
		}
	}

	for _, p := range plan {
		_, _ = fmt.Fprintf(out, "  %s\n", p.dest)
		if opts.DryRun {
			continue
		}
		full := filepath.Join(opts.ModuleRoot, p.dest)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("gen ui: mkdir for %s: %w", p.dest, err)
		}
		if err := os.WriteFile(full, []byte(rewriteRegistryImports(p.content)), 0o644); err != nil {
			return fmt.Errorf("gen ui: write %s: %w", p.dest, err)
		}
	}

	var deps []string
	for _, item := range items {
		deps = append(deps, item.Dependencies...)
	}
	missing, err := missingDeps(opts.ModuleRoot, deps)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		// Printed, not run: gen admin prints its mount line rather than editing
		// serve.go, and the generator does not silently change dependencies.
		_, _ = fmt.Fprintf(out, "\ninstall the packages these components need:\n    pnpm -C frontend add %s\n",
			strings.Join(missing, " "))
	}
	return nil
}
