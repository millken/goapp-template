package commands

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/millken/goapp-template/internal/buildinfo"
)

// homeEnvVar returns the env var name used to override the app home directory.
// e.g. AppName "myapp" → "MYAPP_HOME"
func homeEnvVar() string {
	return strings.ToUpper(buildinfo.AppName) + "_HOME"
}

var homeOnce = sync.OnceValues(func() (string, error) {
	// Allow overriding the entire home dir via env var (e.g. MYAPP_HOME).
	if p := os.Getenv(homeEnvVar()); p != "" {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "."+buildinfo.AppName), nil
})

// Home returns the app home directory.
// Priority: $MYAPP_HOME env var → ~/.myapp
func Home() string {
	h, err := homeOnce()
	if err != nil {
		slog.Warn("failed to determine home directory", "err", err)
		return ""
	}
	return h
}

// ConfigFile returns the path to config.yaml inside Home().
func ConfigFile() string {
	return filepath.Join(Home(), "config.yaml")
}

// EnvFile returns the path to .env inside Home().
func EnvFile() string {
	return filepath.Join(Home(), ".env")
}
