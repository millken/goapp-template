package scaffold

import "path/filepath"

// Admin produces an admin CRUD scaffold: auth-guarded handler + model under
// internal/controller/admin<pkg>/ and index/form Vue pages under
// frontend/pages/admin/<viewdir>/. Routes mount under the admin prefix and the
// resource registers itself in the admin menu. Wire Mount(eng, svc, adm) in
// serve.go after the admin area. No migration is emitted (see Resource).
func Admin(name string, opts Options) error {
	spec, err := prepareSpec(name, opts)
	if err != nil {
		return err
	}
	// Prefix with "admin" so the package name matches its directory
	// (internal/controller/admin<name>); Package feeds both the dir and the
	// template's `package` decl.
	spec.Package = "admin" + spec.Package

	return renderAll([]output{
		{"admin/handler.go.tmpl", filepath.Join("internal/controller", spec.Package, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join("internal/controller", spec.Package, "model.go")},
		{"admin/index.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "index.vue")},
		{"admin/form.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "form.vue")},
	}, spec, opts)
}
