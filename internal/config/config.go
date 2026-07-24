package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/millken/goapp-template/internal/module/admin"
	"github.com/millken/goapp-template/internal/module/db"
	"github.com/millken/goapp-template/internal/module/session"
	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Log    LogConfig    `yaml:"log"`
	Server ServerConfig `yaml:"server"`
	// DB holds the database module config. nil when the db module is not used.
	// config imports the db module here; this is safe from cycles because app
	// (the lifecycle kernel) never imports config, so config→db→app→inertia
	// has no back-edge (§4.4).
	DB *db.Config `yaml:"db"`
	// Session holds the session module config. nil when not used. session
	// imports app (not config), so config→session→app→inertia is acyclic.
	Session *session.Config `yaml:"session"`
	// Admin holds the admin module config. nil when not used. admin imports
	// session and app (not config), so the dependency chain is acyclic.
	Admin *admin.Config `yaml:"admin"`
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	// Addr is the listen address (default: :8080).
	Addr string `yaml:"addr"`
	// StaticPath is the path to the frontend dist directory (dev mode only).
	StaticPath string `yaml:"static_path"`
	// DevAddr is the Vite dev server URL used by the dev proxy.
	DevAddr string `yaml:"dev_addr"`
	// SSRBundlePath is the path to the SSR bundle JS (SSR mode only).
	SSRBundlePath string `yaml:"ssr_bundle_path"`
	// SSR enables server-side rendering via QuickJS (default: false).
	SSR bool `yaml:"ssr"`
}

// LogConfig controls logging behaviour.
type LogConfig struct {
	// Level: debug | info | warn | error  (default: info)
	Level string `yaml:"level"`
	// Format: json | text  (default: text)
	Format string `yaml:"format"`
	// File configures optional log file output. If empty, logs go to stderr.
	File LogFileConfig `yaml:"file"`
}

// LogFileConfig controls file rotation.
type LogFileConfig struct {
	// Path is the log file path. Leave empty to disable file logging.
	Path string `yaml:"path"`
	// MaxSize is the maximum size in bytes before rotation (default: 100MB).
	MaxSize int64 `yaml:"max_size"`
	// MaxBackups is the maximum number of old log files to retain (default: 7).
	MaxBackups int `yaml:"max_backups"`
	// LocalTime uses local time in filenames instead of UTC.
	LocalTime bool `yaml:"local_time"`
}

// Load reads config from the given YAML file path.
// Returns a zero-value Config (with defaults applied) if the file does not exist.
// A read or parse error is returned wrapped with the path so callers can fail
// loudly instead of silently falling back to defaults.
func Load(path string) (Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		Log: LogConfig{
			Level:  "info",
			Format: "text",
			File: LogFileConfig{
				MaxSize:    100 * 1024 * 1024, // 100MB
				MaxBackups: 7,
			},
		},
		Server: ServerConfig{
			Addr: ":8080",
			// StaticPath / SSRBundlePath are only used in non-prod builds (dev
			// reads from disk); under the prod build tag they are no-ops since
			// assets are embedded. The SSR bundle filename must match
			// server.ssrBundleName.
			StaticPath:    "frontend/dist",
			DevAddr:       "http://localhost:5173",
			SSRBundlePath: "frontend/dist/ssr-render-cjs.js",
		},
	}
}
