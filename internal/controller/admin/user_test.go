package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"

	// Register the SQLite driver for tests.
	_ "github.com/millken/goapp-template/internal/driver"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "s3cret" || hash == "" {
		t.Fatalf("hash looks wrong: %q", hash)
	}
	if !verifyPassword(hash, "s3cret") {
		t.Error("verifyPassword should accept the correct password")
	}
	if verifyPassword(hash, "wrong") {
		t.Error("verifyPassword should reject a wrong password")
	}
}

// newUsersDB opens an in-memory SQLite DB with a users table and one seeded user
// (alice / password "pw").
func newUsersDB(t *testing.T) *sqldb.DB {
	t.Helper()
	d, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at BIGINT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	hash, _ := HashPassword("pw")
	if _, err := d.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		"alice", hash, time.Now().UnixNano()); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return d
}

func TestFindUser(t *testing.T) {
	d := newUsersDB(t)
	ctx := context.Background()

	u, err := findUser(ctx, d, "users", "alice")
	if err != nil {
		t.Fatalf("findUser: %v", err)
	}
	if u == nil || u.Username != "alice" {
		t.Fatalf("expected alice, got %+v", u)
	}

	u, err = findUser(ctx, d, "users", "nobody")
	if err != nil {
		t.Fatalf("findUser(nobody): %v", err)
	}
	if u != nil {
		t.Fatalf("expected nil for unknown user, got %+v", u)
	}
}

func TestAuthenticate(t *testing.T) {
	d := newUsersDB(t)
	ctx := context.Background()

	if _, err := authenticate(ctx, d, "users", "alice", "pw"); err != nil {
		t.Errorf("authenticate(correct): %v", err)
	}
	if _, err := authenticate(ctx, d, "users", "alice", "bad"); !errors.Is(err, errInvalidCredentials) {
		t.Errorf("authenticate(wrong pw): want errInvalidCredentials, got %v", err)
	}
	if _, err := authenticate(ctx, d, "users", "nobody", "pw"); !errors.Is(err, errInvalidCredentials) {
		t.Errorf("authenticate(unknown): want errInvalidCredentials, got %v", err)
	}
}
