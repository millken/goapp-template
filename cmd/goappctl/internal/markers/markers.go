// Package markers finds and strips `goappctl:<name>` … `goappctl:end` blocks.
//
// Parsing is deliberately strict: an unclosed, nested, duplicated-end or
// unknown-named block is a hard error naming the file and line. Silently
// mistreating a marker would surface much later as a compile failure in a
// generated project — or, worse, as wrong code that still compiles.
package markers

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Options controls a strip run.
type Options struct {
	// Off names the components whose blocks are deleted. Blocks named by
	// anything else are kept, with only their marker lines removed.
	Off map[string]bool
	// Known lists every valid name; anything else is an error.
	Known map[string]bool
}

// form is one comment syntax a block can be written in.
type form struct {
	open  string // prefix introducing a block, e.g. "//goappctl:"
	end   string // the exact closing line (trimmed), e.g. "//goappctl:end"
	close string // trailing text to trim from the name, e.g. "-->"
}

// forms maps an extension to the comment syntaxes its markers may use. Every
// extension but .vue carries exactly one: .vue carries two, since a block
// opened in its <script setup> half and one opened in its <template> half
// each need their own comment syntax. Strip accepts a block opened in either
// but requires it to close in that same syntax — see the mismatch check
// there.
var forms = map[string][]form{
	".go":   {{open: "//goappctl:", end: "//goappctl:end"}},
	".ts":   {{open: "//goappctl:", end: "//goappctl:end"}},
	".yaml": {{open: "#goappctl:", end: "#goappctl:end"}},
	".yml":  {{open: "#goappctl:", end: "#goappctl:end"}},
	".md":   {{open: "<!--goappctl:", end: "<!--goappctl:end-->", close: "-->"}},
	".html": {{open: "<!--goappctl:", end: "<!--goappctl:end-->", close: "-->"}},
	".css":  {{open: "/*goappctl:", end: "/*goappctl:end*/", close: "*/"}},
	".vue": {
		{open: "//goappctl:", end: "//goappctl:end"},
		{open: "<!--goappctl:", end: "<!--goappctl:end-->", close: "-->"},
	},
}

// matchEnd returns the form among set whose end marker trimmed equals exactly,
// or nil if trimmed closes nothing.
func matchEnd(set []form, trimmed string) *form {
	for i := range set {
		if trimmed == set[i].end {
			return &set[i]
		}
	}
	return nil
}

// matchOpen returns the form among set whose open prefix trimmed carries, or
// nil. Callers check matchEnd first: an end line for one form can carry
// another form's open prefix only if the two share a prefix, which none of
// today's forms do, but the ordering keeps the two concerns separate anyway.
func matchOpen(set []form, trimmed string) *form {
	for i := range set {
		if strings.HasPrefix(trimmed, set[i].open) {
			return &set[i]
		}
	}
	return nil
}

// marker is the substring common to every form, used by HasMarkers.
const marker = "goappctl:"

// HasMarkers reports whether src mentions a marker at all. Callers use it to
// catch markers in file types that have no comment form, which would otherwise
// be shipped verbatim into a generated project.
func HasMarkers(src []byte) bool {
	return bytes.Contains(src, []byte(marker))
}

// Supported reports whether path's extension has a comment form.
func Supported(path string) bool {
	_, ok := forms[filepath.Ext(path)]
	return ok
}

// Strip removes blocks whose name is in opts.Off, unwraps the rest, and
// collapses the blank lines left at each seam (a removed block between two
// blank lines leaves one, not two). Blank lines elsewhere are untouched.
//
// Files whose extension has no comment form are returned unchanged.
func Strip(path string, src []byte, opts Options) ([]byte, int, error) {
	set, ok := forms[filepath.Ext(path)]
	if !ok {
		return src, 0, nil
	}
	if !HasMarkers(src) {
		return src, 0, nil
	}

	// Preserve the exact trailing newline: split/join would otherwise add or
	// drop one depending on the input.
	body, trailer := string(src), ""
	if strings.HasSuffix(body, "\n") {
		body, trailer = strings.TrimSuffix(body, "\n"), "\n"
	}

	var (
		out      []string
		stripped int
		open     string
		openForm *form // the form (comment syntax) that opened the current block
		openLine int
		skipping bool
		seam     bool // a block was just removed; collapse blanks that follow
	)
	for i, line := range strings.Split(body, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)

		// Check every form's end marker first, not just the one that opened
		// the current block: that is what lets a wrong-syntax close be
		// reported as a mismatch instead of falling through as content (or,
		// worse, as a fresh open).
		if ef := matchEnd(set, trimmed); ef != nil {
			if open == "" {
				return nil, 0, fmt.Errorf("%s:%d: %q with no open block", path, lineNo, trimmed)
			}
			if ef != openForm {
				openMarker := openForm.open + open + openForm.close
				return nil, 0, fmt.Errorf(
					"%s:%d: %q closes block %q, but it was opened at line %d with %q; close it with %q instead",
					path, lineNo, trimmed, open, openLine, openMarker, openForm.end)
			}
			if skipping {
				seam = true
			}
			open, openForm, skipping = "", nil, false
			continue
		}

		if of := matchOpen(set, trimmed); of != nil {
			name := strings.TrimPrefix(trimmed, of.open)
			name = strings.TrimSuffix(name, of.close)
			if open != "" {
				return nil, 0, fmt.Errorf("%s:%d: block %q is nested inside %q opened at line %d; nesting is not supported",
					path, lineNo, name, open, openLine)
			}
			if !opts.Known[name] {
				return nil, 0, fmt.Errorf("%s:%d: unknown component %q in marker", path, lineNo, name)
			}
			open, openForm, openLine = name, of, lineNo
			if opts.Off[name] {
				skipping = true
				stripped++
			}
			continue
		}

		if skipping {
			continue
		}
		// At a seam, drop blank lines while the output already ends blank.
		if seam {
			if trimmed == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				continue
			}
			seam = false
		}
		out = append(out, line)
	}

	if open != "" {
		return nil, 0, fmt.Errorf("%s:%d: block %q is unclosed (expected %q)", path, openLine, open, openForm.end)
	}
	return []byte(strings.Join(out, "\n") + trailer), stripped, nil
}

// StripSSRScripts removes the SSR build scripts from package.json and folds
// build:client back into build.
//
// It edits lines rather than re-marshalling (design §11): a round-trip through
// encoding/json would reorder keys and churn the whole file. Dependencies are
// never touched (§4a) — removing one would invalidate pnpm-lock.yaml, and no
// dependency is SSR-only anyway.
func StripSSRScripts(src []byte) ([]byte, error) {
	body, trailer := string(src), ""
	if strings.HasSuffix(body, "\n") {
		body, trailer = strings.TrimSuffix(body, "\n"), "\n"
	}

	var out []string
	sawScripts := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, `"build:ssr"`), strings.HasPrefix(trimmed, `"build:client"`):
			continue
		case strings.HasPrefix(trimmed, `"build"`):
			sawScripts = true
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			comma := ""
			if strings.HasSuffix(trimmed, ",") {
				comma = ","
			}
			out = append(out, indent+`"build": "vite build"`+comma)
		default:
			out = append(out, line)
		}
	}
	if !sawScripts {
		return nil, errors.New(`package.json has no "build" script to rewrite`)
	}

	dropCommaBeforeBrace(out)
	return []byte(strings.Join(out, "\n") + trailer), nil
}

// dropCommaBeforeBrace repairs the JSON that removing a key can leave behind: a
// trailing comma on the line before a closing brace. Mutates out in place.
func dropCommaBeforeBrace(out []string) {
	for i := 0; i+1 < len(out); i++ {
		next := strings.TrimSpace(out[i+1])
		if !strings.HasPrefix(next, "}") {
			continue
		}
		if cur := strings.TrimRight(out[i], " \t"); strings.HasSuffix(cur, ",") {
			out[i] = strings.TrimSuffix(cur, ",")
		}
	}
}
