package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// tableNameRe restricts session table names to a safe identifier, since they
// are interpolated directly into SQL.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// DBStore persists sessions in the application database (production: survives
// restarts, shares state across instances). The table is created on first use.
type DBStore struct {
	db    *sqldb.DB
	table string
}

// NewDBStore wraps a database handle for session storage. table defaults to
// "sessions" and must match ^[A-Za-z_]\w*$.
func NewDBStore(db *sqldb.DB, table string) (*DBStore, error) {
	if table == "" {
		table = "sessions"
	}
	if !tableNameRe.MatchString(table) {
		return nil, fmt.Errorf("session: illegal table name %q", table)
	}
	return &DBStore{db: db, table: table}, nil
}

// ensureTable creates the sessions table if absent. expires_at is BIGINT so it
// holds UnixNano on all dialects (PostgreSQL/MySQL INTEGER is 32-bit).
func (s *DBStore) ensureTable(ctx context.Context) error {
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id         TEXT PRIMARY KEY,
    data       TEXT NOT NULL,
    expires_at BIGINT NOT NULL
)`, s.table)
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
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
		newID, err := randomID()
		if err != nil {
			return "", fmt.Errorf("session: generate id: %w", err)
		}
		id = newID
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("session: encode: %w", err)
	}
	expiresAt := time.Now().Add(ttl).UnixNano()

	q := s.upsertSQL()
	if _, err := s.db.ExecContext(ctx, q, id, string(raw), expiresAt); err != nil {
		return "", fmt.Errorf("session: save: %w", err)
	}
	return id, nil
}

// upsertSQL returns the dialect-correct upsert. The UPDATE branch reuses the
// inserted values, so exactly 3 bind args are needed (id, data, expires_at).
func (s *DBStore) upsertSQL() string {
	switch s.db.Flavor {
	case sqldb.MySQL:
		return fmt.Sprintf(
			`INSERT INTO %s (id, data, expires_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE data = VALUES(data), expires_at = VALUES(expires_at)`,
			s.table,
		)
	default: // SQLite and PostgreSQL both support ON CONFLICT ... DO UPDATE.
		return fmt.Sprintf(
			`INSERT INTO %s (id, data, expires_at) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at`,
			s.table,
		)
	}
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
