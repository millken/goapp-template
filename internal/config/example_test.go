package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplePath is the tracked sample config at the repo root. Unlike config.yaml
// (gitignored, per-developer), this file ships with the repo: `goappctl init`
// strips its markers and copies it to config.yaml.
func examplePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "config.example.yaml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("config.example.yaml missing at repo root: %v", err)
	}
	return p
}

// TestExampleConfig_Parses is the drift guard: every section of the sample
// config must still unmarshal into Config. Without it, renaming a yaml tag
// silently turns a documented key into a no-op.
func TestExampleConfig_Parses(t *testing.T) {
	cfg, err := Load(examplePath(t))
	if err != nil {
		t.Fatalf("Load(config.example.yaml): %v", err)
	}

	t.Run("core", func(t *testing.T) {
		if cfg.Log.Level == "" {
			t.Error("Log.Level is empty; the example should set it explicitly")
		}
		if cfg.Server.Addr == "" {
			t.Error("Server.Addr is empty; the example should set it explicitly")
		}
	})

	//goappctl:ssr
	t.Run("ssr", func(t *testing.T) {
		if cfg.Server.SSRBundlePath == "" {
			t.Error("Server.SSRBundlePath is empty; the example should set it explicitly")
		}
		if cfg.Server.SSR {
			t.Error("Server.SSR = true; the example must default to false (SSR needs a built bundle)")
		}
	})
	//goappctl:end

	//goappctl:db
	t.Run("db", func(t *testing.T) {
		if cfg.DB == nil {
			t.Fatal("DB is nil; the example must carry a db section")
		}
		if cfg.DB.Driver == "" {
			t.Error("DB.Driver is empty")
		}
		if cfg.DB.DSN == "" {
			t.Error("DB.DSN is empty")
		}
		if cfg.DB.Migrations == nil {
			t.Error("DB.Migrations is nil; its presence is what enables auto-migration")
		}
	})
	//goappctl:end

	//goappctl:session
	t.Run("session", func(t *testing.T) {
		if cfg.Session == nil {
			t.Fatal("Session is nil; the example must carry a session section")
		}
		if cfg.Session.Secret == "" {
			t.Error("Session.Secret is empty; it is required")
		}
		if cfg.Session.TTL == 0 {
			t.Error("Session.TTL is zero; the example should set it explicitly")
		}
		if cfg.Session.Store == "" {
			t.Error("Session.Store is empty; the example should set it explicitly")
		}
	})
	//goappctl:end

	//goappctl:admin
	t.Run("admin", func(t *testing.T) {
		if cfg.Admin == nil {
			t.Fatal("Admin is nil; the example must carry an admin section")
		}
		if cfg.Admin.Mount == "" {
			t.Error("Admin.Mount is empty")
		}
		if cfg.Admin.UsersTable == "" {
			t.Error("Admin.UsersTable is empty")
		}
	})
	//goappctl:end

	//goappctl:storage
	t.Run("storage", func(t *testing.T) {
		if cfg.Storage == nil {
			t.Fatal("Storage is nil; the example must carry a storage section")
		}
		if cfg.Storage.Root == "" {
			t.Error("Storage.Root is empty; it is required")
		}
		if cfg.Storage.URLPrefix == "" {
			t.Error("Storage.URLPrefix is empty; the example should set it explicitly")
		}
	})
	//goappctl:end
}

//goappctl:tooling

// TestExampleConfig_MarkersAreWellFormed checks the goappctl marker blocks the
// example carries for `init` (§5.1: unclosed or nested blocks are hard errors).
// It lives here rather than in cmd/goappctl so marker rot fails the template's
// own CI, not just the tool's.
//
// Template-only: it asserts every component still has a block, which is exactly
// what init deletes — hence the `tooling` marker, which init always strips.
func TestExampleConfig_MarkersAreWellFormed(t *testing.T) {
	data, err := os.ReadFile(examplePath(t))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	const (
		openPrefix = "#goappctl:"
		endMarker  = "#goappctl:end"
	)
	// components must each have exactly one block; "tooling" is the reserved
	// always-stripped name and may appear anywhere, including not at all.
	components := map[string]bool{"db": true, "session": true, "admin": true, "ssr": true, "storage": true}
	known := map[string]bool{"tooling": true}
	for name := range components {
		known[name] = true
	}

	open := ""
	openLine := 0
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, openPrefix) {
			continue
		}
		lineNo := i + 1

		if trimmed == endMarker {
			if open == "" {
				t.Errorf("line %d: %q with no open block", lineNo, trimmed)
				continue
			}
			open = ""
			continue
		}

		name := strings.TrimPrefix(trimmed, openPrefix)
		if open != "" {
			t.Errorf("line %d: block %q opened inside still-open block %q (line %d); nesting is not allowed",
				lineNo, name, open, openLine)
			continue
		}
		if !known[name] {
			t.Errorf("line %d: unknown component %q", lineNo, name)
		}
		if seen[name] {
			t.Errorf("line %d: component %q already has a block; keep one block per component", lineNo, name)
		}
		seen[name] = true
		open, openLine = name, lineNo
	}

	if open != "" {
		t.Errorf("block %q opened at line %d is never closed with %q", open, openLine, endMarker)
	}
	for name := range components {
		if !seen[name] {
			t.Errorf("component %q has no marker block; init could not strip its config section", name)
		}
	}
}

//goappctl:end
