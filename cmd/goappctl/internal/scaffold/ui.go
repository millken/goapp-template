package scaffold

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
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
	defer resp.Body.Close()

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
	if registryPath == "" || strings.HasPrefix(registryPath, "/") ||
		strings.HasPrefix(cleanPath, "..") || cleanPath != registryPath {
		return "", fmt.Errorf("registry file path %q is not a plain relative path", registryPath)
	}
	if strings.HasPrefix(registryPath, "ui/") {
		return "frontend/src/components/" + registryPath, nil
	}
	return "frontend/src/" + registryPath, nil
}

// rewriteRegistryImports points registry-internal imports at where the files
// actually land.
func rewriteRegistryImports(content string) string {
	return strings.ReplaceAll(content, registryImportPrefix, "@/components/ui")
}
