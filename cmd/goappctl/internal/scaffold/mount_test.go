package scaffold

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const mountGenSrc = `// MountAll wires every non-admin controller area onto the engine.
package controller

import (
	"github.com/me/demo/internal/app"
	"github.com/me/demo/internal/controller/site"
	"github.com/millken/inertia"
)

func MountAll(eng *inertia.Engine, svc *app.Services) {
	// gen:mounts:begin
	site.Mount(eng, svc)
	// gen:mounts:end
}
`

func mustParse(t *testing.T, src []byte) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "mount_gen.go", src, parser.ParseComments); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, src)
	}
}

func TestEditMountRegion(t *testing.T) {
	out, added, err := EditMountRegion([]byte(mountGenSrc), "github.com/me/demo", "post")
	if err != nil {
		t.Fatalf("EditMountRegion: %v", err)
	}
	if !added {
		t.Error("added = false, want true")
	}
	mustParse(t, out)
	s := string(out)
	if !strings.Contains(s, `"github.com/me/demo/internal/controller/post"`) {
		t.Errorf("import not added:\n%s", s)
	}
	if !strings.Contains(s, "post.Mount(eng, svc)") {
		t.Errorf("mount call not added:\n%s", s)
	}
	if !strings.Contains(s, "site.Mount(eng, svc)") {
		t.Errorf("existing mount call lost:\n%s", s)
	}
	// The call must land inside the region, or the next edit would not find it.
	begin := strings.Index(s, "gen:mounts:begin")
	end := strings.Index(s, "gen:mounts:end")
	at := strings.Index(s, "post.Mount")
	if at < begin || at > end {
		t.Errorf("post.Mount landed outside the gen:mounts region:\n%s", s)
	}
}

// TestEditMountRegion_Idempotent covers re-running gen for an existing
// resource: the region must not collect duplicate calls.
func TestEditMountRegion_Idempotent(t *testing.T) {
	once, _, err := EditMountRegion([]byte(mountGenSrc), "github.com/me/demo", "post")
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}
	twice, added, err := EditMountRegion(once, "github.com/me/demo", "post")
	if err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if added {
		t.Error("added = true on re-run, want false")
	}
	if string(twice) != string(once) {
		t.Errorf("re-run changed the file:\n%s", twice)
	}
	if n := strings.Count(string(twice), "post.Mount"); n != 1 {
		t.Errorf("post.Mount appears %d times, want 1", n)
	}
}

func TestEditMountRegion_KeepsCallsSorted(t *testing.T) {
	src := []byte(mountGenSrc)
	for _, pkg := range []string{"zebra", "alpha", "post"} {
		out, _, err := EditMountRegion(src, "github.com/me/demo", pkg)
		if err != nil {
			t.Fatalf("add %s: %v", pkg, err)
		}
		src = out
	}
	mustParse(t, src)
	region := string(src)
	region = region[strings.Index(region, "gen:mounts:begin"):strings.Index(region, "gen:mounts:end")]
	var order []string
	for _, line := range strings.Split(region, "\n") {
		if i := strings.Index(line, ".Mount(eng, svc)"); i > 0 {
			order = append(order, strings.TrimSpace(line[:i]))
		}
	}
	want := []string{"alpha", "post", "site", "zebra"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("region order = %v, want %v", order, want)
	}
}

func TestEditMountRegion_MissingRegion(t *testing.T) {
	src := strings.ReplaceAll(mountGenSrc, "// gen:mounts:begin", "")
	_, _, err := EditMountRegion([]byte(src), "github.com/me/demo", "post")
	if err == nil || !strings.Contains(err.Error(), "gen:mounts") {
		t.Fatalf("expected a missing-region error, got %v", err)
	}
}

// TestEditMountRegion_EmptyRegion covers a project whose sample site area was
// removed, leaving the region with no calls at all.
func TestEditMountRegion_EmptyRegion(t *testing.T) {
	src := `package controller

import (
	"github.com/me/demo/internal/app"
	"github.com/millken/inertia"
)

func MountAll(eng *inertia.Engine, svc *app.Services) {
	// gen:mounts:begin
	// gen:mounts:end
}
`
	out, added, err := EditMountRegion([]byte(src), "github.com/me/demo", "post")
	if err != nil {
		t.Fatalf("EditMountRegion: %v", err)
	}
	if !added {
		t.Error("added = false, want true")
	}
	mustParse(t, out)
	if !strings.Contains(string(out), `"github.com/me/demo/internal/controller/post"`) {
		t.Errorf("import not added:\n%s", out)
	}
}
