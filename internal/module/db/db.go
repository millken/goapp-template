// Package db is a feature module providing database access and migrations via
// github.com/dnsoa/go/sqldb.
//
// It is an app.Module: Register is a no-op (pure service, no routes); Boot
// opens the connection pool, pings it, and runs migrations when configured;
// Shutdown closes the pool. Other modules depend on the Provider interface
// (DB() *sqldb.DB) and call it lazily from handlers — the value is only valid
// after Boot.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/goapp-template/internal/app"
)

// migrationFS holds the migration SQL files bundled with this package. The FS
// comes from the package (not from yaml, which cannot carry an fs.FS), so
// configuring a `migrations:` section can never silently no-op due to a nil FS.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Provider exposes the database handle. Consumers depend on this interface
// (accept interfaces), not on the concrete *Module. DB() is only valid after
// Boot; calling it before Boot panics with a clear message (preferable to a nil
// dereference deep inside a handler).
type Provider interface {
	DB() *sqldb.DB
}

// Config configures the db module. It is a pointer in New so that Boot can
// enforce the enable-consistency rule (§4.4): a module that is constructed and
// Use'd must have its config section present; a nil cfg yields a clear error
// rather than a silent skip.
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

// Module is the db feature module.
type Module struct {
	cfg *Config
	db  *sqldb.DB
}

// New constructs the db module. cfg is a pointer so Boot can distinguish
// "enabled but misconfigured" (nil → error) from "not enabled" (not Use'd).
func New(cfg *Config) *Module { return &Module{cfg: cfg} }

// Register is a no-op: db is a pure service with no routes.
func (m *Module) Register(a *app.App) error { return nil }

// Boot opens the pool, pings it, applies pool settings, and runs migrations if
// configured. On any failure it closes the pool it opened and returns the error
// (so it is never left half-initialised, and app.Serve excludes it from the
// Shutdown set — see app.shutdownN).
func (m *Module) Boot(ctx context.Context) error {
	if m.cfg == nil {
		return errors.New("db: module enabled but [db] config section missing")
	}

	d, err := sqldb.Open(m.cfg.Driver, m.cfg.DSN, sqldb.WithDebug(m.cfg.Debug))
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}

	if m.cfg.MaxOpenConns > 0 {
		d.SetMaxOpenConns(m.cfg.MaxOpenConns)
	}
	if m.cfg.MaxIdleConns > 0 {
		d.SetMaxIdleConns(m.cfg.MaxIdleConns)
	}
	if m.cfg.ConnMaxLifetime > 0 {
		d.SetConnMaxLifetime(m.cfg.ConnMaxLifetime)
	}

	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return fmt.Errorf("db ping: %w", err)
	}
	m.db = d

	if mg := m.cfg.Migrations; mg != nil {
		if err := m.migrate(ctx, mg); err != nil {
			d.Close()
			m.db = nil
			return err
		}
	}
	return nil
}

// migrate runs up-migrations using the package-embedded FS.
func (m *Module) migrate(ctx context.Context, mg *Migrations) error {
	var opts []sqldb.MigrationOption
	if t := mg.Table; t != "" {
		opts = append(opts, sqldb.WithMigrationTable(t))
	}
	if s := mg.Service; s != "" {
		opts = append(opts, sqldb.WithMigrationService(s))
	}
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("db migrate: resolve embedded migrations: %w", err)
	}
	if err := m.db.MigrateUp(ctx, sub, opts...); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}
	return nil
}

// DB returns the database handle. It panics if called before Boot (the value is
// meaningless and a nil dereference inside a handler would be far harder to
// diagnose).
func (m *Module) DB() *sqldb.DB {
	if m.db == nil {
		panic("db: DB() called before Boot")
	}
	return m.db
}

// Shutdown closes the pool. Safe to call when Boot never set the handle.
func (m *Module) Shutdown(context.Context) error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}
