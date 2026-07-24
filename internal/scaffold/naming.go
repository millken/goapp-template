// Package scaffold is the dev-time code generator for the Go + Inertia + Vue
// CRUD pattern. It is tooling, NOT a runtime app.Module — hence it lives under
// internal/scaffold, not internal/module.
//
// It provides (1) resource-name normalization (Spec/NewSpec) shared by the
// generators, (2) Resource(name) — a public CRUD resource scaffolder
// (`goapp gen resource <name>`), and (3) Admin(name) — an admin CRUD resource
// scaffolder (`goapp gen admin <name>`). Generated resources are standalone
// app.Modules wired explicitly in commands/serve.go.
package scaffold

import (
	"fmt"
	"strings"
	"unicode"
)

// Spec holds the derived identifiers for a resource, used by the generators to
// fill templates consistently. All fields are computed from the input resource
// name by NewSpec.
type Spec struct {
	// Resource is the raw input name as given on the CLI (e.g. "blog-post").
	Resource string
	// Type is the PascalCase Go type name (e.g. "BlogPost").
	Type string
	// Receiver is a short, valid Go receiver name (lowercase, e.g. "bp").
	Receiver string
	// Package is the lowercased package/import path segment (e.g. "blogpost").
	Package string
	// Table is the snake_case database table name (e.g. "blog_posts").
	Table string
	// Route is the URL path prefix (e.g. "blog-posts").
	Route string
	// FileBase is the snake_case file-name stem for migrations (e.g. "blog_post").
	FileBase string
	// ViewDir is the frontend pages subdirectory (e.g. "blog-post").
	ViewDir string
}

// NewSpec derives all identifier forms from a resource name. The input may be
// any of snake_case, kebab-case, CamelCase, or space-separated words; it is
// normalized to words first, then each form is built from the words.
func NewSpec(resource string) (Spec, error) {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return Spec{}, fmt.Errorf("mvc: resource name is empty")
	}
	if !isAlphaNum(resource) {
		return Spec{}, fmt.Errorf("mvc: resource name %q must be alphanumeric (dashes/underscores/spaces allowed)", resource)
	}

	words := splitWords(resource)
	if len(words) == 0 {
		return Spec{}, fmt.Errorf("mvc: resource name %q yielded no words", resource)
	}

	spec := Spec{
		Resource: resource,
		Type:     joinPascal(words),
		Package:  strings.ToLower(strings.Join(words, "")),
		Table:    joinSnake(words),
		Route:    joinKebab(words),
		FileBase: joinSnake(words),
		ViewDir:  joinKebab(words),
	}
	spec.Receiver = receiver(spec.Type)
	return spec, nil
}

// isAlphaNum reports whether s contains only letters, digits, dash, underscore,
// or space — the separators splitWords understands.
func isAlphaNum(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == ' ':
		default:
			return false
		}
	}
	return true
}

// splitWords breaks a name into lowercase words on dashes, underscores, spaces,
// and camelCase boundaries (e.g. "BlogPost" → ["blog","post"]).
func splitWords(s string) []string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	var out []string
	for _, field := range strings.Fields(s) {
		out = append(out, camelSplit(field)...)
	}
	return out
}

// camelSplit splits a CamelCase / PascalCase token into lowercase words.
func camelSplit(s string) []string {
	var words []string
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) && b.Len() > 0 {
			words = append(words, strings.ToLower(b.String()))
			b.Reset()
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		words = append(words, strings.ToLower(b.String()))
	}
	return words
}

func joinPascal(words []string) string {
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(w[1:])
	}
	return b.String()
}

func joinSnake(words []string) string {
	return strings.Join(words, "_")
}

func joinKebab(words []string) string {
	return strings.Join(words, "-")
}

// receiver derives a short, valid Go receiver identifier from a PascalCase type
// name. It prefers the lowercase first letter; if that collides with a Go
// keyword/builtin it would need adjustment, but for resource names this is
// practically fine. For single-word types it uses the whole lowercased word
// when short (<=3 chars) to stay readable.
func receiver(pascal string) string {
	if pascal == "" {
		return "m"
	}
	r := strings.ToLower(pascal[:1])
	// Avoid the common predeclared identifiers that read badly as receivers.
	switch r {
	case "i", "l", "o": // easily confused with literals
		if len(pascal) > 1 {
			return strings.ToLower(pascal[:2])
		}
	}
	return r
}
