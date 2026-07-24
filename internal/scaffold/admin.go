package scaffold

import (
	"fmt"
	"path/filepath"
)

// Admin produces an admin CRUD resource scaffold for the given name: an
// auth-guarded handler + model under internal/module/admin<pkg>/ and index/form
// Vue pages under frontend/pages/admin/<viewdir>/. Routes mount under the admin
// prefix and the resource registers itself in the admin menu. Invoked by
// `goapp gen admin <name>`.
//
// The model reuses the resource model template verbatim (same struct/table
// shape). Migrations are not generated per resource — see Resource; add them by
// hand under internal/module/db/migrations/.
func Admin(name string, opts Options) error {
	spec, err := NewSpec(name)
	if err != nil {
		return err
	}
	// Prefix the package identifier with "admin" so the generated Go package
	// name matches its directory (internal/module/admin<name>) and the wiring
	// hint (a.Use(admin<name>.New(...))) resolves as written. Package feeds both
	// the directory and the template's `package` decl, keeping all three aligned.
	spec.Package = "admin" + spec.Package

	type out struct{ tmpl, path string }
	outputs := []out{
		{"admin/handler.go.tmpl", filepath.Join("internal/module", spec.Package, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join("internal/module", spec.Package, "model.go")},
		{"admin/index.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "index.vue")},
		{"admin/form.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "form.vue")},
	}
	for _, o := range outputs {
		if err := render(o.tmpl, o.path, spec, opts); err != nil {
			return fmt.Errorf("scaffold: generate %s: %w", o.path, err)
		}
	}
	return nil
}
