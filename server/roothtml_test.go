//go:build !prod

package server

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The dark class must be set before first paint or the page flashes light. The
// script lives in rootHTML as a marker-wrapped Go assignment — a marker inside
// the HTML string literal would be served to browsers as text.
func TestRootHTML_CarriesTheDarkBootScript(t *testing.T) {
	html := rootHTML(fstest.MapFS{})
	for _, want := range []string{"localStorage.theme", "prefers-color-scheme", "classList.add('dark')"} {
		if !strings.Contains(html, want) {
			t.Errorf("rootHTML missing %q", want)
		}
	}
	// The marker prefix is split across two literals on purpose: this file is
	// admin-owned but still ships when admin is selected, and a contiguous
	// "goappctl" + ":" in its own source would trip the generator's blanket
	// leaked-marker scanner even though it is not an actual marker line.
	if strings.Contains(html, "goappctl"+":") {
		t.Error("a goappctl marker leaked into the served HTML")
	}
}
