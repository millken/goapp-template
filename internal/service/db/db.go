// Package db is an infrastructure service providing database access and
// migrations via github.com/dnsoa/go/sqldb.
//
// It implements app.Lifecycle: Start opens the connection pool, pings it, and
// runs migrations when configured; Stop closes the pool. It registers no routes
// and imports no app kernel — serve.go drives Start/Stop explicitly and hands
// the opened *sqldb.DB to app.Services.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/dnsoa/go/sqldb"
)

// migrationFS holds the migration SQL files bundled with this package. The FS
// comes from the package (not from yaml, which cannot carry an fs.FS), so
// configuring a `migrations:` section can never silently no-op due to a nil FS.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Provider exposes the database handle. Consumers depend on this interface
// (accept interfaces), not on the concrete *Service. DB() is only valid after
// Start; calling it before Start panics with a clear message (preferable to a
// nil dereference deep inside a handler).
type Provider interface {
	DB() *sqldb.DB
}

// Config configures the db service. It is a pointer in New so that Start can
// enforce the enable-consistency rule: a service that is constructed and Started
// must have its config section present; a nil cfg yields a clear error rather
// than a silent skip.
type Config struct {
	Driver          string        `yaml:"driver"` // "sqlite3" / "mysql" / "pgx" …
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open"`
	MaxIdleConns    int           `yaml:"max_idle"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	Debug           bool          `yaml:"debug"`
	Migrations      *Migrations   `yaml:"migrations"` // presence of this section enables migrations
}

// Migrations holds the yaml-expressible migration options. The fs.FS is NOT
// here — it cannot come from yaml; it is the package-level migrationFS.
type Migrations struct {
	Table   string `yaml:"table"`   // default "migrations" (sqldb default, not schema_migrations)
	Service string `yaml:"service"` // default "default"; isolates histories when services share a DB
}

// Service is the db infrastructure service.
type Service struct {
	cfg *Config
	db  *sqldb.DB
}

// New constructs the db service. cfg is a pointer so Start can distinguish
// "enabled but misconfigured" (nil → error) from "not enabled" (never Started).
func New(cfg *Config) *Service { return &Service{cfg: cfg} }

// Start opens the pool, pings it, applies pool settings, and runs migrations if
// configured. On any failure it closes the pool it opened and returns the error
// (so it is never left half-initialised).
func (s *Service) Start(ctx context.Context) error {
	if s.cfg == nil {
		return errors.New("db: service enabled but [db] config section missing")
	}

	d, err := sqldb.Open(s.cfg.Driver, s.cfg.DSN, sqldb.WithDebug(s.cfg.Debug))
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}

	if s.cfg.MaxOpenConns > 0 {
		d.SetMaxOpenConns(s.cfg.MaxOpenConns)
	}
	if s.cfg.MaxIdleConns > 0 {
		d.SetMaxIdleConns(s.cfg.MaxIdleConns)
	}
	if s.cfg.ConnMaxLifetime > 0 {
		d.SetConnMaxLifetime(s.cfg.ConnMaxLifetime)
	}

	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return fmt.Errorf("db ping: %w", err)
	}
	s.db = d

	if mg := s.cfg.Migrations; mg != nil {
		if err := s.migrate(ctx, mg); err != nil {
			d.Close()
			s.db = nil
			return err
		}
	}
	return nil
}

// migrate runs up-migrations using the package-embedded FS.
func (s *Service) migrate(ctx context.Context, mg *Migrations) error {
	var opts []sqldb.MigrationOption
	if t := mg.Table; t != "" {
		opts = append(opts, sqldb.WithMigrationTable(t))
	}
	if svc := mg.Service; svc != "" {
		opts = append(opts, sqldb.WithMigrationService(svc))
	}
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("db migrate: resolve embedded migrations: %w", err)
	}
	if err := s.db.MigrateUp(ctx, sub, opts...); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}
	return nil
}

// DB returns the database handle. It panics if called before Start (the value
// is meaningless and a nil dereference inside a handler would be far harder to
// diagnose).
func (s *Service) DB() *sqldb.DB {
	if s.db == nil {
		panic("db: DB() called before Start")
	}
	return s.db
}

// Stop closes the pool. Safe to call when Start never set the handle.
func (s *Service) Stop(context.Context) error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
