// MountAll wires every non-admin controller area onto the engine. Admin is
// wired separately in serve.go because it needs its own config.
//
// The gen:mounts block below is the registration point for generated
// resources. `gen resource` does not edit it — it prints the Mount line and you
// add it here by hand. Keep the block markers intact so tooling can find it.
package controller

import (
	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/controller/site"
	"github.com/millken/inertia"
)

// MountAll registers all generated controller areas. Route uniqueness is
// enforced by inertia at registration time.
func MountAll(eng *inertia.Engine, svc *app.Services) {
	// gen:mounts:begin
	site.Mount(eng, svc)
	// gen:mounts:end
}
