package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dnsoa/go/sqldb"

	//goappctl:db
	// The DB-store tests below open a real SQLite connection, so they need the
	// driver registered. Blank imports must stay inside a marker — goimports
	// cannot drop them.
	_ "github.com/millken/goapp-template/internal/driver"
	//goappctl:end
)

// TestUpsertSQL_PlaceholderCount guards that every dialect's upsert has exactly
// 3 '?' placeholders — Save passes 3 args, and a mismatch passes on SQLite but
// errors on pq/pgx/MySQL.
func TestUpsertSQL_PlaceholderCount(t *testing.T) {
	for _, flavor := range []sqldb.Flavor{sqldb.SQLite, sqldb.PostgreSQL, sqldb.MySQL} {
		s := &DBStore{db: &sqldb.DB{Flavor: flavor}, table: "sessions"}
		if n := strings.Count(s.upsertSQL(), "?"); n != 3 {
			t.Errorf("flavor %v: upsert has %d placeholders, want 3 (Save passes 3 args)", flavor, n)
		}
	}
}

// TestMemoryStore_Lifecycle exercises save/load/delete on the in-memory store.
func TestMemoryStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// Save assigns an id for empty input.
	values := map[string]any{"user": "alice", "count": float64(3)}
	id, err := s.Save(ctx, "", values, time.Hour)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Load returns the saved values.
	got, _, ok, err := s.Load(ctx, id)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got["user"] != "alice" {
		t.Fatalf("got user %v, want alice", got["user"])
	}

	// Save with the same id updates in place.
	values["user"] = "bob"
	if _, err := s.Save(ctx, id, values, time.Hour); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, _, _, _ = s.Load(ctx, id)
	if got["user"] != "bob" {
		t.Fatalf("after update: got user %v, want bob", got["user"])
	}

	// Delete removes it.
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, _, ok, _ = s.Load(ctx, id)
	if ok {
		t.Fatal("expected session to be gone after delete")
	}
}

func TestMemoryStore_Expiry(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	id, _ := s.Save(ctx, "", map[string]any{"k": "v"}, 10*time.Millisecond)
	_, _, ok, _ := s.Load(ctx, id)
	if !ok {
		t.Fatal("expected session present immediately")
	}
	time.Sleep(30 * time.Millisecond)
	_, _, ok, _ = s.Load(ctx, id)
	if ok {
		t.Fatal("expected session expired")
	}
}

func TestMemoryStore_LoadUnknown(t *testing.T) {
	s := NewMemoryStore()
	_, _, ok, err := s.Load(context.Background(), "no-such-id")
	if err != nil || ok {
		t.Fatalf("expected not-found (ok=false, err=nil), got ok=%v err=%v", ok, err)
	}
}

//goappctl:db

// TestDBStore_Lifecycle exercises save/load/delete on the db store.
func TestDBStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s, err := NewDBStore(db, "sessions_test")
	if err != nil {
		t.Fatalf("NewDBStore: %v", err)
	}
	if err := s.ensureTable(ctx); err != nil {
		t.Fatalf("ensureTable: %v", err)
	}

	values := map[string]any{"role": "admin", "n": float64(42)}
	id, err := s.Save(ctx, "", values, time.Hour)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	got, _, ok, err := s.Load(ctx, id)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got["role"] != "admin" {
		t.Fatalf("got role %v, want admin", got["role"])
	}

	// Update via Save with same id.
	values["role"] = "user"
	if _, err := s.Save(ctx, id, values, time.Hour); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, _, _, _ = s.Load(ctx, id)
	if got["role"] != "user" {
		t.Fatalf("after update: got role %v, want user", got["role"])
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, _, ok, _ = s.Load(ctx, id)
	if ok {
		t.Fatal("expected session gone after delete")
	}
}

func TestDBStore_Expiry(t *testing.T) {
	ctx := context.Background()
	db, _ := sqldb.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })
	s, _ := NewDBStore(db, "sessions_test")
	_ = s.ensureTable(ctx)

	id, _ := s.Save(ctx, "", map[string]any{"k": "v"}, 10*time.Millisecond)
	_, _, ok, _ := s.Load(ctx, id)
	if !ok {
		t.Fatal("expected present immediately")
	}
	time.Sleep(30 * time.Millisecond)
	_, _, ok, _ = s.Load(ctx, id)
	if ok {
		t.Fatal("expected expired")
	}
}

func TestNewDBStore_InvalidTableName(t *testing.T) {
	db, _ := sqldb.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })
	cases := []string{
		"has space",
		"name;drop--",
		`quoted"name`,
	}
	for _, name := range cases {
		if _, err := NewDBStore(db, name); err == nil {
			t.Errorf("expected error for table name %q, got nil", name)
		}
	}
	// Empty defaults to "sessions" and is valid.
	if _, err := NewDBStore(db, ""); err != nil {
		t.Errorf("empty name should default, got %v", err)
	}
	if _, err := NewDBStore(db, "sessions_test_2"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

// TestUpsertSQL_PerFlavor verifies the dialect-correct upsert without live
// MySQL/PostgreSQL instances.
func TestUpsertSQL_PerFlavor(t *testing.T) {
	db, _ := sqldb.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })

	// SQLite flavor (default from open).
	s, _ := NewDBStore(db, "sessions")
	got := s.upsertSQL()
	if !strings.Contains(got, "ON CONFLICT(id)") {
		t.Fatalf("sqlite upsert: expected ON CONFLICT, got %q", got)
	}

	// Simulate MySQL flavor.
	s.db.Flavor = sqldb.MySQL
	got = s.upsertSQL()
	if !strings.Contains(got, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("mysql upsert: expected ON DUPLICATE KEY UPDATE, got %q", got)
	}

	// PostgreSQL uses the ON CONFLICT branch.
	s.db.Flavor = sqldb.PostgreSQL
	got = s.upsertSQL()
	if !strings.Contains(got, "ON CONFLICT(id)") {
		t.Fatalf("postgres upsert: expected ON CONFLICT, got %q", got)
	}
}

// TestCreateTableSQL_PerFlavor pins the one part of the DDL that cannot be
// shared: MySQL refuses a TEXT primary key without a prefix length, so id has to
// be VARCHAR there. Without a live MySQL in the suite this is the only place the
// mistake would be caught before deployment.
func TestCreateTableSQL_PerFlavor(t *testing.T) {
	db, _ := sqldb.Open("sqlite3", ":memory:")
	t.Cleanup(func() { db.Close() })

	s, _ := NewDBStore(db, "sessions")
	if got := s.createTableSQL(); !strings.Contains(got, "id         TEXT PRIMARY KEY") {
		t.Errorf("sqlite DDL: expected a TEXT id, got %q", got)
	}

	s.db.Flavor = sqldb.PostgreSQL
	if got := s.createTableSQL(); !strings.Contains(got, "id         TEXT PRIMARY KEY") {
		t.Errorf("postgres DDL: expected a TEXT id, got %q", got)
	}

	s.db.Flavor = sqldb.MySQL
	got := s.createTableSQL()
	// 64, not more: randomID is 32 bytes of hex, so the column is exact.
	if !strings.Contains(got, "id         VARCHAR(64) PRIMARY KEY") {
		t.Errorf("mysql DDL: expected a VARCHAR(64) id, got %q", got)
	}
	if !strings.Contains(got, "ENGINE=InnoDB") {
		t.Errorf("mysql DDL: expected an InnoDB table, got %q", got)
	}
}

//goappctl:end
