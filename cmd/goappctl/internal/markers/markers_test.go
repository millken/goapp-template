package markers

import (
	"encoding/json"
	"strings"
	"testing"
)

func opts(off ...string) Options {
	o := Options{
		Off:   map[string]bool{},
		Known: map[string]bool{"db": true, "session": true, "admin": true, "ssr": true, "tooling": true},
	}
	for _, n := range off {
		o.Off[n] = true
	}
	return o
}

func TestStrip_RemovesUnselectedBlock(t *testing.T) {
	src := "package main\n\n//goappctl:db\nvar db = 1\n//goappctl:end\nvar keep = 2\n"
	got, n, err := Strip("x.go", []byte(src), opts("db"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	want := "package main\n\nvar keep = 2\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStrip_UnwrapsSelectedBlock(t *testing.T) {
	src := "package main\n\n//goappctl:db\nvar db = 1\n//goappctl:end\n"
	got, n, err := Strip("x.go", []byte(src), opts())
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 0 {
		t.Errorf("stripped = %d, want 0", n)
	}
	want := "package main\n\nvar db = 1\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStrip_CollapsesSeamBlankLines covers §5.6: a block surrounded by blank
// lines must leave one blank line, not two, and must not touch blank lines
// elsewhere in the file.
func TestStrip_CollapsesSeamBlankLines(t *testing.T) {
	src := "a\n\n\nb\n\n//goappctl:db\nx\n//goappctl:end\n\nc\n"
	got, _, err := Strip("x.go", []byte(src), opts("db"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	// The a/b gap (two blank lines) is untouched; the seam collapses to one.
	want := "a\n\n\nb\n\nc\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStrip_YAMLForm(t *testing.T) {
	src := "log:\n  level: info\n#goappctl:db\ndb:\n  driver: sqlite3\n#goappctl:end\nserver:\n  addr: :8080\n"
	got, _, err := Strip("config.yaml", []byte(src), opts("db"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if strings.Contains(string(got), "driver") || strings.Contains(string(got), "goappctl") {
		t.Errorf("db section not stripped: %q", got)
	}
	if !strings.Contains(string(got), "addr: :8080") {
		t.Errorf("server section lost: %q", got)
	}
}

// TestStrip_IndentedMarkers matters because config.example.yaml nests the ssr
// keys inside server:, so its markers are indented.
func TestStrip_IndentedMarkers(t *testing.T) {
	src := "server:\n  addr: :8080\n  #goappctl:ssr\n  ssr: false\n  #goappctl:end\n"
	got, n, err := Strip("config.yaml", []byte(src), opts("ssr"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	want := "server:\n  addr: :8080\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStrip_MarkdownForm(t *testing.T) {
	src := "# Title\n\n<!--goappctl:ssr-->\n## SSR\ntext\n<!--goappctl:end-->\n\n## Other\n"
	got, n, err := Strip("README.md", []byte(src), opts("ssr"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	if strings.Contains(string(got), "## SSR") || strings.Contains(string(got), "goappctl") {
		t.Errorf("SSR section not stripped: %q", got)
	}
	if !strings.Contains(string(got), "## Other") {
		t.Errorf("other section lost: %q", got)
	}
}

func TestStrip_MultipleBlocksSameName(t *testing.T) {
	src := "//goappctl:session\na\n//goappctl:end\nkeep\n//goappctl:session\nb\n//goappctl:end\n"
	got, n, err := Strip("x.go", []byte(src), opts("session"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 2 {
		t.Errorf("stripped = %d, want 2", n)
	}
	if string(got) != "keep\n" {
		t.Errorf("got %q, want %q", got, "keep\n")
	}
}

func TestStrip_NoTrailingNewlinePreserved(t *testing.T) {
	src := "a\n//goappctl:db\nx\n//goappctl:end\nb"
	got, _, err := Strip("x.go", []byte(src), opts("db"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if string(got) != "a\nb" {
		t.Errorf("got %q, want %q", got, "a\nb")
	}
}

func TestStrip_UnsupportedExtensionIsNoop(t *testing.T) {
	src := "<template>x</template>\n"
	got, n, err := Strip("Page.vue", []byte(src), opts("db"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 0 || string(got) != src {
		t.Errorf("expected no-op, got %q (%d)", got, n)
	}
}

func TestStrip_Errors(t *testing.T) {
	cases := []struct {
		name, src, wantErr string
	}{
		{
			name:    "unclosed",
			src:     "a\n//goappctl:db\nx\n",
			wantErr: "unclosed",
		},
		{
			name:    "nested",
			src:     "//goappctl:db\n//goappctl:session\nx\n//goappctl:end\n//goappctl:end\n",
			wantErr: "nested",
		},
		{
			name:    "unknown name",
			src:     "//goappctl:redis\nx\n//goappctl:end\n",
			wantErr: "unknown component",
		},
		{
			name:    "orphan end",
			src:     "a\n//goappctl:end\n",
			wantErr: "no open block",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Strip("x.go", []byte(c.src), opts("db"))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), c.wantErr)
			}
			// Errors must locate the problem for the template author.
			if !strings.Contains(err.Error(), "x.go:") {
				t.Errorf("error = %q, want a file:line prefix", err.Error())
			}
		})
	}
}

// TestHasMarkers guards the safety net: a marker in a file type with no
// comment form would otherwise be shipped verbatim.
func TestHasMarkers(t *testing.T) {
	if !HasMarkers([]byte("<template>\n<!--goappctl:admin-->\n</template>")) {
		t.Error("expected markers to be detected")
	}
	if HasMarkers([]byte("no markers here")) {
		t.Error("false positive")
	}
}

func TestStripSSRScripts(t *testing.T) {
	src := `{
  "name": "myapp-frontend",
  "scripts": {
    "dev": "vite",
    "build": "run-s build:client build:ssr",
    "build:client": "vite build",
    "build:ssr": "vite build --config vite.config.ssr.ts",
    "type-check": "vue-tsc --noEmit"
  },
  "dependencies": {
    "vue": "^3.5.40"
  }
}
`
	got, err := StripSSRScripts([]byte(src))
	if err != nil {
		t.Fatalf("StripSSRScripts: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "build:ssr") || strings.Contains(s, "build:client") {
		t.Errorf("ssr scripts still present:\n%s", s)
	}
	if !strings.Contains(s, `"build": "vite build"`) {
		t.Errorf("build script not rewritten:\n%s", s)
	}
	// §4a: dependencies are never touched (pnpm-lock.yaml must stay valid).
	if !strings.Contains(s, `"vue": "^3.5.40"`) {
		t.Errorf("dependencies were modified:\n%s", s)
	}
	// §11: formatting is preserved, not re-marshalled.
	if !strings.Contains(s, `  "name": "myapp-frontend",`) {
		t.Errorf("formatting churned:\n%s", s)
	}
	if !strings.Contains(s, `    "dev": "vite",`) {
		t.Errorf("indentation churned:\n%s", s)
	}
}

// TestStripSSRScripts_StaysValidJSON is the guard for the hand-rolled
// trailing-comma fixup: removing a key that was last in its object would
// otherwise leave `"x": "y",` before the closing brace.
func TestStripSSRScripts_StaysValidJSON(t *testing.T) {
	cases := map[string]string{
		"ssr in the middle": `{
  "scripts": {
    "build": "run-s build:client build:ssr",
    "build:client": "vite build",
    "build:ssr": "vite build --config vite.config.ssr.ts",
    "type-check": "vue-tsc --noEmit"
  }
}
`,
		"ssr last in object": `{
  "scripts": {
    "dev": "vite",
    "build": "run-s build:client build:ssr",
    "build:client": "vite build",
    "build:ssr": "vite build --config vite.config.ssr.ts"
  }
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := StripSSRScripts([]byte(src))
			if err != nil {
				t.Fatalf("StripSSRScripts: %v", err)
			}
			var v map[string]any
			if err := json.Unmarshal(got, &v); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, got)
			}
			scripts := v["scripts"].(map[string]any)
			if _, ok := scripts["build:ssr"]; ok {
				t.Error("build:ssr survived")
			}
			if scripts["build"] != "vite build" {
				t.Errorf("build = %v, want %q", scripts["build"], "vite build")
			}
		})
	}
}

func TestStripSSRScripts_Idempotent(t *testing.T) {
	src := "{\n  \"scripts\": {\n    \"dev\": \"vite\",\n    \"build\": \"vite build\"\n  }\n}\n"
	got, err := StripSSRScripts([]byte(src))
	if err != nil {
		t.Fatalf("StripSSRScripts: %v", err)
	}
	if string(got) != src {
		t.Errorf("expected no change, got %q", got)
	}
}

func TestStrip_CSSForm(t *testing.T) {
	src := "@import \"tailwindcss\";\n\n/*goappctl:admin*/\n:root { --x: 1; }\n/*goappctl:end*/\nbody { color: red; }\n"

	got, n, err := Strip("main.css", []byte(src), opts("admin"))
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 1 {
		t.Errorf("stripped = %d, want 1", n)
	}
	if want := "@import \"tailwindcss\";\n\nbody { color: red; }\n"; string(got) != want {
		t.Errorf("admin off: got %q, want %q", got, want)
	}

	got, n, err = Strip("main.css", []byte(src), opts())
	if err != nil {
		t.Fatalf("Strip: %v", err)
	}
	if n != 0 {
		t.Errorf("stripped = %d, want 0", n)
	}
	want := "@import \"tailwindcss\";\n\n:root { --x: 1; }\nbody { color: red; }\n"
	if string(got) != want {
		t.Errorf("admin on: got %q, want %q", got, want)
	}
}

func TestSupported_CSS(t *testing.T) {
	if !Supported("frontend/src/styles/main.css") {
		t.Error("Supported(.css) = false, want true")
	}
}

func TestStripAdminDeps(t *testing.T) {
	src := []byte(`{
  "dependencies": {
    "@tanstack/vue-table": "^8.21.3",
    "@vueuse/core": "^13.0.0",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "lucide-vue-next": "^0.544.0",
    "reka-ui": "^2.10.1",
    "tailwind-merge": "^3.3.1",
    "vue": "^3.5.40"
  },
  "devDependencies": {
    "tw-animate-css": "^1.4.0",
    "vite": "^8.1.5"
  }
}
`)

	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	for _, gone := range []string{
		"@tanstack/vue-table", "@vueuse/core", "class-variance-authority",
		"clsx", "lucide-vue-next", "reka-ui", "tailwind-merge",
	} {
		if _, ok := pkg.Dependencies[gone]; ok {
			t.Errorf("dependencies still has %q", gone)
		}
	}
	if _, ok := pkg.DevDependencies["tw-animate-css"]; ok {
		t.Error("devDependencies still has tw-animate-css")
	}
	if pkg.Dependencies["vue"] != "^3.5.40" {
		t.Errorf("vue was not preserved: %q", pkg.Dependencies["vue"])
	}
	if pkg.DevDependencies["vite"] != "^8.1.5" {
		t.Errorf("vite was not preserved: %q", pkg.DevDependencies["vite"])
	}
}

func TestStripAdminDeps_Idempotent(t *testing.T) {
	src := []byte("{\n  \"dependencies\": {\n    \"vue\": \"^3.5.40\"\n  }\n}\n")
	out, err := StripAdminDeps(src)
	if err != nil {
		t.Fatalf("StripAdminDeps: %v", err)
	}
	if string(out) != string(src) {
		t.Errorf("a package.json with no admin deps must be untouched:\ngot  %q\nwant %q", out, src)
	}
}
