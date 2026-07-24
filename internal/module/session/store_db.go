package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// DBStore persists sessions in the application database via db.Provider. Suitable
// for production: survives restarts and shares state across instances.
//
// The sessions table is created on first use (idempotent CREATE TABLE IF NOT
// EXISTS); no migration file is required from the db module.
type DBStore struct {
	db    *sqldb.DB
	table string
}

// NewDBStore wraps a database handle for session storage. table is the sessions
// table name (default "sessions").
func NewDBStore(db *sqldb.DB, table string) *DBStore {
	if table == "" {
		table = "sessions"
	}
	return &DBStore{db: db, table: table}
}

// ensureTable creates the sessions table if it does not exist. Idempotent.
func (s *DBStore) ensureTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS %s (
    id         TEXT PRIMARY KEY,
    data       TEXT NOT NULL,
    expires_at INTEGER NOT NULL
)`
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(ddl, s.table)); err != nil {
		return fmt.Errorf("session: create table %s: %w", s.table, err)
	}
	return nil
}
func (s *DBStore) Load(ctx context.Context, id string) (map[string]any, time.Time, bool, error) {
	var (
		raw       string
		expiresAt int64
	)
	q := fmt.Sprintf(`SELECT data, expires_at FROM %s WHERE id = ?`, s.table)
	if err := s.db.QueryRowContext(ctx, q, id).Scan(&raw, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, fmt.Errorf("session: load: %w", err)
	}
	exp := time.Unix(0, expiresAt)
	if time.Now().After(exp) {
		// Expired: best-effort delete, treat as absent.
		_, _ = s.delete(ctx, id)
		return nil, time.Time{}, false, nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("session: decode: %w", err)
	}
	return values, exp, true, nil
}

func (s *DBStore) Save(ctx context.Context, id string, values map[string]any, ttl time.Duration) (string, error) {
	if id == "" {
		id = newID()
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("session: encode: %w", err)
	}
	expiresAt := time.Now().Add(ttl).UnixNano()
	// Upsert: insert-or-replace covers SQLite/PostgreSQL (ON CONFLICT) and
	// MySQL (REPLACE) via the simplest portable form. The data and expiry are
	// always refreshed.
	q := fmt.Sprintf(
		`INSERT INTO %s (id, data, expires_at) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at`,
		s.table,
	)
	if _, err := s.db.ExecContext(ctx, q, id, string(raw), expiresAt); err != nil {
		// Some drivers/dialects may not support ON CONFLICT; fall back to a
		// delete+insert so the store still works on MySQL without REPEATABLE.
		if _, fallbackErr := s.fallbackUpsert(ctx, id, string(raw), expiresAt); fallbackErr != nil {
			return "", fmt.Errorf("session: save: %w (fallback: %v)", err, fallbackErr)
		}
	}
	return id, nil
}

// fallbackUpsert handles dialects whose ON CONFLICT syntax differs: delete then
// insert. Used only when the primary upsert errors.
func (s *DBStore) fallbackUpsert(ctx context.Context, id, raw string, expiresAt int64) (sql.Result, error) {
	if _, err := s.delete(ctx, id); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`INSERT INTO %s (id, data, expires_at) VALUES (?, ?, ?)`, s.table)
	return s.db.ExecContext(ctx, q, id, raw, expiresAt)
}

func (s *DBStore) Delete(ctx context.Context, id string) error {
	if _, err := s.delete(ctx, id); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	return nil
}

func (s *DBStore) delete(ctx context.Context, id string) (sql.Result, error) {
	q := fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, s.table)
	return s.db.ExecContext(ctx, q, id)
}
