// Package scaffold is the dev-time code generator for the Go + Inertia + Vue
// CRUD pattern. It provides resource-name normalization (Spec/NewSpec) and two
// scaffolders: Resource (`goapp gen resource`) and Admin (`goapp gen admin`).
package scaffold

import (
	"fmt"
	"strings"
	"unicode"
)

// Spec holds the derived identifiers for a resource, computed by NewSpec.
type Spec struct {
	Resource string // raw input name (e.g. "blog-post")
	Type     string // PascalCase Go type name (e.g. "BlogPost")
	Receiver string // short lowercase Go receiver (e.g. "bp")
	Package  string // lowercased package segment (e.g. "blogpost")
	Table    string // snake_case table name (e.g. "blog_posts")
	Route    string // URL path prefix (e.g. "blog-posts")
	FileBase string // snake_case migration stem (e.g. "blog_post")
	ViewDir  string // frontend pages subdir (e.g. "blog-post")
	// Module is the target project's module path, used for the generated
	// imports. It comes from the project's go.mod, not from this package —
	// generated code must import the project it lands in, not the template.
	Module string
}

// NewSpec derives all identifier forms from a resource name (snake_case,
// kebab-case, CamelCase, or space-separated words are all accepted).
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
// or space (the separators splitWords understands).
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
// and camelCase boundaries.
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

// receiver derives a short Go receiver identifier from a PascalCase type name,
// avoiding single letters that read as literals (i, l, o).
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
