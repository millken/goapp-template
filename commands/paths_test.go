package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/millken/goapp-template/internal/buildinfo"
)

func TestHomeEnvVar(t *testing.T) {
	saved := buildinfo.AppName
	t.Cleanup(func() { buildinfo.AppName = saved })

	cases := []struct {
		appName string
		want    string
	}{
		{"myapp", "MYAPP_HOME"},
		{"goapp-template", "GOAPP-TEMPLATE_HOME"},
		{"a", "A_HOME"},
	}
	for _, c := range cases {
		t.Run(c.appName, func(t *testing.T) {
			buildinfo.AppName = c.appName
			if got := homeEnvVar(); got != c.want {
				t.Errorf("homeEnvVar() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestResolveHome_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(homeEnvVar(), dir)

	got, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome() returned error: %v", err)
	}
	if got != dir {
		t.Errorf("resolveHome() = %q, want %q (from env)", got, dir)
	}
}

func TestResolveHome_FallsBackToUserHomeDir(t *testing.T) {
	// Ensure the env override is NOT set so we exercise the fallback path.
	t.Setenv(homeEnvVar(), "")

	got, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome() returned error: %v", err)
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("os.UserHomeDir() failed in this environment: %v", err)
	}
	want := filepath.Join(userHome, "."+buildinfo.AppName)
	if got != want {
		t.Errorf("resolveHome() = %q, want %q", got, want)
	}
}

func TestResolveHome_EmptyEnvIgnored(t *testing.T) {
	// An empty MYAPP_HOME should be treated as unset (falls back to user home).
	t.Setenv(homeEnvVar(), "")

	got, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome() returned error: %v", err)
	}
	if got == "" {
		t.Errorf("resolveHome() = empty string; want a real path")
	}
}

// TestConfigAndEnvFilePaths verifies that ConfigFile() and EnvFile() build
// their paths on top of Home(). Both share the package-level homeOnce cache,
// so we test them together with a single env value.
func TestConfigAndEnvFilePaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(homeEnvVar(), dir)

	// Compute expected values directly from resolveHome (bypasses the cache)
	// to assert the path-construction logic independently.
	home, err := resolveHome()
	if err != nil {
		t.Fatalf("resolveHome(): %v", err)
	}
	wantConfig := filepath.Join(home, "config.yaml")
	wantEnv := filepath.Join(home, ".env")

	if got, err := ConfigFile(); err != nil || got != wantConfig {
		t.Errorf("ConfigFile() = (%q, %v), want (%q, nil)", got, err, wantConfig)
	}
	if got, err := EnvFile(); err != nil || got != wantEnv {
		t.Errorf("EnvFile() = (%q, %v), want (%q, nil)", got, err, wantEnv)
	}
}
