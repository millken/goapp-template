package scaffold

import (
	"fmt"
	"path/filepath"
)

// Admin produces an admin CRUD scaffold: auth-guarded handler + model under
// internal/controller/admin<pkg>/ and index/form Vue pages under
// frontend/pages/admin/<viewdir>/. Routes mount under the admin prefix and the
// resource registers itself in the admin menu. Wire Mount(eng, svc, adm) in
// serve.go after the admin area. No migration is emitted (see Resource).
func Admin(name string, opts Options) error {
	if opts.Module == "" {
		return fmt.Errorf("scaffold: Options.Module is required (the target project's module path)")
	}
	spec, err := NewSpec(name)
	if err != nil {
		return err
	}
	spec.Module = opts.Module
	// Prefix with "admin" so the package name matches its directory
	// (internal/controller/admin<name>); Package feeds both the dir and the
	// template's `package` decl.
	spec.Package = "admin" + spec.Package

	type out struct{ tmpl, path string }
	outputs := []out{
		{"admin/handler.go.tmpl", filepath.Join("internal/controller", spec.Package, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join("internal/controller", spec.Package, "model.go")},
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
