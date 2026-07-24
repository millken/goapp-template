package admin

import (
	"testing"

	"github.com/millken/inertia"
)

// newTestEngine builds a production-mode inertia engine for tests.
func newTestEngine(t *testing.T) *inertia.Engine {
	t.Helper()
	eng, err := inertia.New(inertia.WithMode(inertia.ModeProduction))
	if err != nil {
		t.Fatalf("inertia.New: %v", err)
	}
	return eng
}

func TestValidate(t *testing.T) {
	// nil config → error (enable-consistency: a wired admin area must be
	// configured). Services is not consulted by Validate, so nil is fine here.
	if err := New(nil, nil).Validate(); err == nil {
		t.Error("nil [admin] config should error")
	}
	// illegal users-table identifier → error (it is interpolated into SQL).
	if err := New(nil, &Config{UsersTable: "bad table"}).Validate(); err == nil {
		t.Error("illegal users table name should error")
	}
	// a present config with defaults → ok.
	if err := New(nil, &Config{}).Validate(); err != nil {
		t.Errorf("valid config should pass, got %v", err)
	}
}

func TestResolvedDefaults(t *testing.T) {
	a := New(nil, &Config{})
	if a.Prefix() != "/admin" {
		t.Errorf("Prefix default: got %q", a.Prefix())
	}
	if a.LoginPath() != "/admin/login" {
		t.Errorf("LoginPath default: got %q", a.LoginPath())
	}
	if a.authKey() != defaultAuthKey {
		t.Errorf("authKey default: got %q", a.authKey())
	}
	if a.usersTable() != "users" {
		t.Errorf("usersTable default: got %q", a.usersTable())
	}
}
