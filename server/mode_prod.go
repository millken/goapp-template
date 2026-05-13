//go:build prod

package server

import (
	"embed"
	"io/fs"

	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/inertia"
)

var defaultMode = inertia.ModeProduction

//go:embed embedded/dist
var embeddedDist embed.FS

func loadSSRBundle(_ config.ServerConfig) (string, error) {
	data, err := embeddedDist.ReadFile("embedded/dist/ssr-render-cjs.js")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func staticFS(_ config.ServerConfig) (fs.FS, error) {
	return fs.Sub(embeddedDist, "embedded/dist")
}
