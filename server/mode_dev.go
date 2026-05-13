//go:build !prod

package server

import (
	"io/fs"
	"os"

	"github.com/millken/goapp-template/internal/config"
	"github.com/millken/inertia"
)

var defaultMode = inertia.ModeDevelopment

func loadSSRBundle(cfg config.ServerConfig) (string, error) {
	data, err := os.ReadFile(cfg.SSRBundlePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func staticFS(cfg config.ServerConfig) (fs.FS, error) {
	return os.DirFS(cfg.StaticPath), nil
}
