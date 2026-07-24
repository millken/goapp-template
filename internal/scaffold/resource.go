package scaffold

import (
	"fmt"
	"path/filepath"
)

// Resource produces a full public CRUD resource scaffold for the given name:
// a handler + model under internal/module/<pkg>/ and index/form Vue pages under
// frontend/pages/<viewdir>/. Invoked by `goapp gen resource <name>`.
//
// No migration is emitted here. Schema changes are event-driven and usually
// holistic (not one-per-resource), and the db module only embeds and applies
// internal/module/db/migrations/. A per-resource migrations/ dir would be a dead
// file that nothing runs. Add versioned migrations by hand under
// internal/module/db/migrations/ (NNN_name.up.sql / .down.sql); sqldb applies
// them in filename order and records applied versions.
func Resource(name string, opts Options) error {
	spec, err := NewSpec(name)
	if err != nil {
		return err
	}

	type out struct{ tmpl, path string }
	outputs := []out{
		{"resource/handler.go.tmpl", filepath.Join("internal/module", spec.Package, "handler.go")},
		{"resource/model.go.tmpl", filepath.Join("internal/module", spec.Package, "model.go")},
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
