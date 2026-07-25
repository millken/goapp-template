package scaffold

import (
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"text/template"
)

//go:embed templates
var templateFS embed.FS

// Options controls generator behaviour.
type Options struct {
	// Force overwrites existing files (default: refuse, to preserve manual edits).
	Force bool
	// ModuleRoot is the repo root containing internal/ and frontend/; empty means
	// paths are relative to the cwd.
	ModuleRoot string
	// Module is the target project's module path (from its go.mod). Required:
	// the generated handlers import <Module>/internal/app, so a wrong or empty
	// value produces code that cannot compile.
	Module string
}

// output pairs an embedded template with its destination path.
type output struct{ tmpl, path string }

// prepareSpec validates opts and derives the spec shared by every scaffolder.
func prepareSpec(name string, opts Options) (Spec, error) {
	if opts.Module == "" {
		return Spec{}, fmt.Errorf("scaffold: Options.Module is required (the target project's module path)")
	}
	spec, err := NewSpec(name)
	if err != nil {
		return Spec{}, err
	}
	spec.Module = opts.Module
	return spec, nil
}

// renderAll writes every output, naming the destination on failure.
func renderAll(outputs []output, spec Spec, opts Options) error {
	for _, o := range outputs {
		if err := render(o.tmpl, o.path, spec, opts); err != nil {
			return fmt.Errorf("scaffold: generate %s: %w", o.path, err)
		}
	}
	return nil
}

// render parses one embedded template (e.g. "resource/handler.go.tmpl") and
// writes it to outPath. Templates use [[ .Field ]] delimiters so Vue's {{ }}
// passes through verbatim.
func render(tmplName, outPath string, spec Spec, opts Options) error {
	raw, err := templateFS.ReadFile(path.Join("templates", tmplName))
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmplName, err)
	}
	tmpl, err := template.New(tmplName).Delims("[[", "]]").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplName, err)
	}

	full := outPath
	if opts.ModuleRoot != "" {
		full = filepath.Join(opts.ModuleRoot, outPath)
	}
	if !opts.Force {
		if _, err := os.Stat(full); err == nil {
			return fmt.Errorf("file exists (use --force to overwrite): %s", full)
		}
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.Create(full)
	if err != nil {
		return fmt.Errorf("create %s: %w", full, err)
	}
	defer f.Close()
	return tmpl.Execute(f, spec)
}
