package scaffold

import "path/filepath"

// AdminDir is the admin area's controller directory, relative to the project
// root. Generated admin resources nest inside it, one package per resource.
const AdminDir = "internal/controller/admin"

// Admin produces an admin CRUD scaffold: auth-guarded handler + model under
// internal/controller/admin/<pkg>/ and index/form Vue pages under
// frontend/pages/admin/<viewdir>/. Routes mount under the admin prefix and the
// resource registers itself in the admin menu. Wire Mount(eng, svc, adm) in
// serve.go after the admin area. No migration is emitted (see Resource).
//
// The Go tree mirrors the Vue tree — both nest the resource under admin — so the
// package name is the bare resource name and still matches its directory. A
// public resource of the same name is a different package at
// internal/controller/<pkg>/ with the same package name; no generated file
// imports both, and one that does needs an import alias.
func Admin(name string, opts Options) error {
	spec, err := prepareSpec(name, opts)
	if err != nil {
		return err
	}
	dir := filepath.Join(AdminDir, spec.Package)

	return renderAll([]output{
		{"admin/handler.go.tmpl", filepath.Join(dir, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join(dir, "model.go")},
		{"admin/index.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "index.vue")},
		{"admin/form.vue.tmpl", filepath.Join("frontend/pages/admin", spec.ViewDir, "form.vue")},
	}, spec, opts)
}
