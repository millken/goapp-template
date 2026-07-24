package session

import (
	"context"
	"testing"
	"time"

	_ "github.com/millken/goapp-template/internal/driver"
	"github.com/dnsoa/go/sqldb"
)

// TestMemoryStore_Lifecycle exercises save/load/delete and expiry on the
// in-memory store.
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

// TestDBStore_Lifecycle exercises save/load/delete on the db store using an
// in-memory SQLite database.
func TestDBStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := sqldb.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s := NewDBStore(db, "sessions_test")
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
	s := NewDBStore(db, "sessions_test")
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
