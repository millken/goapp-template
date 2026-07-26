package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dnsoa/go/sqldb"
	_ "github.com/mattn/go-sqlite3"
)

// groupFixture builds the two tables this package reads and returns an open
// handle. It creates them directly rather than running the migrator, so a
// failure here points at the query rather than at migration wiring.
func groupFixture(t *testing.T) *sqldb.DB {
	t.Helper()
	d, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	for _, q := range []string{
		`CREATE TABLE user_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			superuser INTEGER NOT NULL DEFAULT 0,
			permissions TEXT NOT NULL DEFAULT '[]',
			created_at BIGINT NOT NULL)`,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			group_id INTEGER REFERENCES user_groups(id),
			status INTEGER NOT NULL DEFAULT 1)`,
		`INSERT INTO user_groups (id, name, superuser, permissions, created_at)
		 VALUES (1, 'Administrators', 1, '[]', 0)`,
		`INSERT INTO user_groups (id, name, superuser, permissions, created_at)
		 VALUES (2, 'Editors', 0, '["post.modify","user.access"]', 0)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (1, 'root', 'x', 0, 1)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (2, 'editor', 'x', 0, 2)`,
		`INSERT INTO users (id, username, password_hash, created_at, group_id)
		 VALUES (3, 'orphan', 'x', 0, NULL)`,
	} {
		if _, err := d.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("fixture %q: %v", q, err)
		}
	}
	return d
}

func TestFindGroup(t *testing.T) {
	d := groupFixture(t)
	ctx := context.Background()

	su, err := findCaller(ctx, d, "users", 1)
	if err != nil {
		t.Fatalf("superuser: %v", err)
	}
	if !su.group.Superuser {
		t.Error("user 1 should be a superuser")
	}
	if su.username != "root" {
		t.Errorf("username = %q, want root", su.username)
	}

	ed, err := findCaller(ctx, d, "users", 2)
	if err != nil {
		t.Fatalf("editor: %v", err)
	}
	if ed.group.Superuser {
		t.Error("user 2 should not be a superuser")
	}
	if !ed.group.Permissions.Allows("post.modify") {
		t.Error("editor should hold post.modify")
	}
	if !ed.group.Permissions.Allows("post.access") {
		t.Error("post.modify should imply post.access")
	}
	if ed.group.Permissions.Allows("user.modify") {
		t.Error("editor holds only user.access, so user.modify must fail")
	}
}

// Fail closed: no group row is not the same as an empty permission set, but both
// deny — and the caller must be able to tell this apart from a database error.
func TestFindGroup_NoGroupIsErrNoGroup(t *testing.T) {
	d := groupFixture(t)

	if _, err := findCaller(context.Background(), d, "users", 3); !errors.Is(err, errNoGroup) {
		t.Errorf("orphaned user: got %v, want errNoGroup", err)
	}
	if _, err := findCaller(context.Background(), d, "users", 999); !errors.Is(err, errNoGroup) {
		t.Errorf("unknown user: got %v, want errNoGroup", err)
	}
}

// A malformed permissions column is a storage problem, not a denial — the caller
// turns errNoGroup into 403 and anything else into 500, so these must differ.
func TestFindGroup_BadJSONIsNotErrNoGroup(t *testing.T) {
	d := groupFixture(t)
	if _, err := d.ExecContext(context.Background(),
		`UPDATE user_groups SET permissions = 'not json' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	_, err := findCaller(context.Background(), d, "users", 2)
	if err == nil {
		t.Fatal("want an error for malformed JSON")
	}
	if errors.Is(err, errNoGroup) {
		t.Error("malformed JSON must not be reported as a missing group")
	}
}

func TestFindGroupID(t *testing.T) {
	d := groupFixture(t)
	ctx := context.Background()

	id, err := FindGroupID(ctx, d, "Editors")
	if err != nil {
		t.Fatalf("Editors: %v", err)
	}
	if id != 2 {
		t.Errorf("Editors id = %d, want 2", id)
	}

	_, err = FindGroupID(ctx, d, "Nope")
	if err == nil {
		t.Fatal("want an error for an unknown group")
	}
	// The message has to name both what was asked for and what exists, because
	// this is the error an operator meets when bootstrapping.
	for _, want := range []string{"Nope", "Administrators", "--group"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// The unknown-name message tells the operator to pass a different --group. If a
// storage failure produced that same message they would keep retrying names
// against a database that is not answering, so the two must not collapse.
func TestFindGroupID_StorageFailureIsNotAnUnknownName(t *testing.T) {
	d := groupFixture(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := FindGroupID(context.Background(), d, "Administrators")
	if err == nil {
		t.Fatal("want an error once the pool is closed")
	}
	if strings.Contains(err.Error(), "--group") {
		t.Errorf("a storage failure must not be reported as an unknown group name: %v", err)
	}
}
