package config

import (
	"errors"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Log LogConfig `yaml:"log"`
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
func Load(path string) (Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
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
	}
}
