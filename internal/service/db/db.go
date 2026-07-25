// Package db is the database infrastructure service (connection pool +
// migrations via github.com/dnsoa/go/sqldb). It implements app.Lifecycle:
// Start opens/pings/migrates, Stop closes. It imports no app kernel — serve.go
// drives Start/Stop and hands the opened *sqldb.DB to app.Services.
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
// comes from the package, not yaml (which cannot carry an fs.FS).
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Provider exposes the database handle. DB() panics if called before Start
// (preferable to a nil dereference inside a handler).
type Provider interface {
	DB() *sqldb.DB
}

// Config configures the db service. A pointer in New lets Start distinguish
// "enabled but misconfigured" (nil) from "not enabled" (never Started).
type Config struct {
	Driver          string        `yaml:"driver"` // "sqlite3" / "mysql" / "pgx" …
	DSN             string        `yaml:"dsn"`
	MaxOpenConns    int           `yaml:"max_open"`
	MaxIdleConns    int           `yaml:"max_idle"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
	Debug           bool          `yaml:"debug"`
	Migrations      *Migrations   `yaml:"migrations"` // presence enables migrations
}

// Migrations holds the yaml-expressible migration options; the fs.FS is the
// package-level migrationFS.
type Migrations struct {
	Table   string `yaml:"table"`   // default "migrations"
	Service string `yaml:"service"` // isolates histories when services share a DB
}

// Service is the db infrastructure service.
type Service struct {
	cfg *Config
	db  *sqldb.DB
}

// New constructs the db service.
func New(cfg *Config) *Service { return &Service{cfg: cfg} }

// Start opens the pool, pings it, applies pool settings, and runs migrations if
// configured. On failure it closes the pool it opened.
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

// DB returns the handle, panicking if called before Start.
func (s *Service) DB() *sqldb.DB {
	if s.db == nil {
		panic("db: DB() called before Start")
	}
	return s.db
}

// Stop closes the pool; safe to call if Start never set the handle.
func (s *Service) Stop(context.Context) error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
