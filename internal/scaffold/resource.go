package scaffold

import (
	"fmt"
	"path/filepath"
)

// Resource produces a public CRUD scaffold: handler + model under
// internal/controller/<pkg>/ and index/form Vue pages under
// frontend/pages/<viewdir>/. Wire the handler once into controller.MountAll.
//
// No migration is emitted; add versioned migrations by hand under
// internal/service/db/migrations/ (NNN_name.up.sql / .down.sql).
func Resource(name string, opts Options) error {
	spec, err := NewSpec(name)
	if err != nil {
		return err
	}

	type out struct{ tmpl, path string }
	outputs := []out{
		{"resource/handler.go.tmpl", filepath.Join("internal/controller", spec.Package, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join("internal/controller", spec.Package, "model.go")},
		{"resource/index.vue.tmpl", filepath.Join("frontend/pages", spec.ViewDir, "index.vue")},
		{"resource/form.vue.tmpl", filepath.Join("frontend/pages", spec.ViewDir, "form.vue")},
	}
	for _, o := range outputs {
		if err := render(o.tmpl, o.path, spec, opts); err != nil {
			return fmt.Errorf("scaffold: generate %s: %w", o.path, err)
		}
	}
	return nil
}
