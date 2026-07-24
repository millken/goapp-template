package commands

import (
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

// resolveHome computes the app home directory from the environment.
// Priority: $MYAPP_HOME env var → ~/.<AppName>
func resolveHome() (string, error) {
	// Allow overriding the entire home dir via env var (e.g. MYAPP_HOME).
	if p := os.Getenv(homeEnvVar()); p != "" {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "."+buildinfo.AppName), nil
}

var homeOnce = sync.OnceValues(resolveHome)

// Home returns the app home directory.
// Priority: $MYAPP_HOME env var → ~/.myapp
// Returns an error if the home directory cannot be determined, so callers
// can fail loudly instead of silently operating on the filesystem root.
func Home() (string, error) {
	return homeOnce()
}

// ConfigFile returns the path to config.yaml inside Home().
func ConfigFile() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "config.yaml"), nil
}

// EnvFile returns the path to .env inside Home().
func EnvFile() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".env"), nil
}
