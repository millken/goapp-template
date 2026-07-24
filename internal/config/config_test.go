package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := defaults()

	t.Run("log defaults", func(t *testing.T) {
		if d.Log.Level != "info" {
			t.Errorf("Log.Level = %q, want %q", d.Log.Level, "info")
		}
		if d.Log.Format != "text" {
			t.Errorf("Log.Format = %q, want %q", d.Log.Format, "text")
		}
		const wantSize int64 = 100 * 1024 * 1024
		if d.Log.File.MaxSize != wantSize {
			t.Errorf("Log.File.MaxSize = %d, want %d", d.Log.File.MaxSize, wantSize)
		}
		if d.Log.File.MaxBackups != 7 {
			t.Errorf("Log.File.MaxBackups = %d, want %d", d.Log.File.MaxBackups, 7)
		}
	})

	t.Run("server defaults", func(t *testing.T) {
		if d.Server.Addr != ":8080" {
			t.Errorf("Server.Addr = %q, want %q", d.Server.Addr, ":8080")
		}
		if d.Server.StaticPath != "frontend/dist" {
			t.Errorf("Server.StaticPath = %q, want %q", d.Server.StaticPath, "frontend/dist")
		}
		if d.Server.DevAddr != "http://localhost:5173" {
			t.Errorf("Server.DevAddr = %q, want %q", d.Server.DevAddr, "http://localhost:5173")
		}
		if d.Server.SSRBundlePath != "frontend/dist/ssr-render-cjs.js" {
			t.Errorf("Server.SSRBundlePath = %q, want %q", d.Server.SSRBundlePath, "frontend/dist/ssr-render-cjs.js")
		}
		if d.Server.SSR {
			t.Errorf("Server.SSR = true, want false")
		}
	})
}

func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("Load(nonexistent) returned error: %v", err)
	}
	// Should equal defaults().
	d := defaults()
	if cfg != d {
		t.Errorf("Load(nonexistent) = %+v, want %+v", cfg, d)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	const yaml = `
log:
  level: debug
  format: json
  file:
    path: /tmp/app.log
server:
  addr: ":9090"
  ssr: true
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "debug")
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "json")
	}
	if cfg.Log.File.Path != "/tmp/app.log" {
		t.Errorf("Log.File.Path = %q, want %q", cfg.Log.File.Path, "/tmp/app.log")
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Server.Addr = %q, want %q", cfg.Server.Addr, ":9090")
	}
	if !cfg.Server.SSR {
		t.Errorf("Server.SSR = false, want true")
	}
}

// TestLoad_PartialFileKeepsDefaults verifies that yaml.v3 merges at the
// field level: a user config that only sets log.file.path must NOT clobber
// the default MaxSize/MaxBackups.
func TestLoad_PartialFileKeepsDefaults(t *testing.T) {
	const yaml = `
log:
  file:
    path: /tmp/app.log
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	const wantSize int64 = 100 * 1024 * 1024
	if cfg.Log.File.MaxSize != wantSize {
		t.Errorf("Log.File.MaxSize = %d, want default %d (field should be preserved)", cfg.Log.File.MaxSize, wantSize)
	}
	if cfg.Log.File.MaxBackups != 7 {
		t.Errorf("Log.File.MaxBackups = %d, want default 7 (field should be preserved)", cfg.Log.File.MaxBackups)
	}
}

func TestLoad_InvalidYAMLReturnsError(t *testing.T) {
	const invalidYAML = `
log:
  level: [this is
  not valid: yaml: {{{}}}
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(invalidYAML), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err == nil {
		t.Fatalf("Load(invalid yaml) returned nil error; want a parse error")
	}
	// Error must mention the path so users can locate the bad file.
	var pathErr interface{ Unwrap() []error }
	_ = pathErr
	if !contains(err.Error(), "config.yaml") {
		t.Errorf("error %q should contain the config file path", err.Error())
	}
	// Returned config should still be usable (defaults applied).
	if cfg.Server.Addr != ":8080" {
		t.Errorf("returned cfg.Server.Addr = %q, want default :8080", cfg.Server.Addr)
	}
}

func TestLoad_UnreadableFileReturnsError(t *testing.T) {
	// Create a directory where a file is expected — ReadFile will fail.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatalf("Load(directory) returned nil error; want a read error")
	}
	if !contains(err.Error(), "read config") {
		t.Errorf("error %q should be wrapped as a read error", err.Error())
	}
}

// TestLoad_NotExistIsNotError ensures a missing config file is the normal
// path (uses defaults) and does NOT surface as an error.
func TestLoad_NotExistIsNotError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "definitely-missing.yaml")

	// Sanity check: file truly doesn't exist.
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition failed: %s should not exist", path)
	}

	_, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing file) = error %v; want nil (missing config is normal)", err)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
