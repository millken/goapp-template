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
	// Force overwrites existing files. By default the generator refuses to
	// overwrite, so manual edits are preserved.
	Force bool
	// ModuleRoot is the repo root containing internal/ and frontend/. It is used
	// to resolve output paths. When empty, paths are relative to the cwd.
	ModuleRoot string
}

// render parses one embedded template (by slash path relative to templates/,
// e.g. "resource/handler.go.tmpl") and writes it to outPath.
//
// Vue templates use {{ }} for interpolation, which collides with text/template's
// default delimiters. To keep Vue files readable, templates use [[ .Field ]] for
// Go-side substitutions while Vue's {{ }} passes through verbatim.
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
