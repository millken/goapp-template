// Package db is the database infrastructure service (connection pool +
// migrations via github.com/dnsoa/go/sqldb). It implements app.Lifecycle:
// Start opens/pings/migrates, Stop closes. It imports no app kernel — serve.go
// drives Start/Stop and hands the opened *sqldb.DB to app.Services.
//
// SQLite, MySQL and PostgreSQL are all supported, and which one is in use is a
// property of the DSN rather than of the build: internal/driver registers all
// three, Config.Driver may be left empty to be inferred from the DSN's shape
// (dialect.go), and the migrations are carried once per dialect so Start can
// select the right set from the opened handle's flavor. Queries elsewhere in the
// app therefore hold one spelling — `?` placeholders, which sqldb rewrites to
// `$1` for PostgreSQL. See migrations/README.md for the DDL differences that
// this split exists to absorb.
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
// One subdirectory per dialect — see migrationsFor and migrations/README.md.
//
//go:embed migrations/*/*.sql
var migrationFS embed.FS

// Provider exposes the database handle. DB() panics if called before Start
// (preferable to a nil dereference inside a handler).
type Provider interface {
	DB() *sqldb.DB
}

// Config configures the db service. A pointer in New lets Start distinguish
// "enabled but misconfigured" (nil) from "not enabled" (never Started).
type Config struct {
	// Driver is "sqlite3", "mysql" or "pgx". Leave it empty to infer the driver
	// from the shape of DSN (see resolveDriver); an explicit value always wins.
	Driver          string        `yaml:"driver"`
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
		return errors.New("db: service enabled but [db] config section missing " +
			"(copy that section from config.example.yaml)")
	}

	driver, err := resolveDriver(s.cfg.Driver, s.cfg.DSN)
	if err != nil {
		return err
	}

	d, err := sqldb.Open(driver, s.cfg.DSN, sqldb.WithDebug(s.cfg.Debug))
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

// migrate runs up-migrations using the dialect's slice of the embedded FS.
func (s *Service) migrate(ctx context.Context, mg *Migrations) error {
	if err := checkMultiStatements(s.db.Flavor, s.cfg.DSN); err != nil {
		return err
	}

	var opts []sqldb.MigrationOption
	if t := mg.Table; t != "" {
		opts = append(opts, sqldb.WithMigrationTable(t))
	}
	if svc := mg.Service; svc != "" {
		opts = append(opts, sqldb.WithMigrationService(svc))
	}
	sub, err := migrationsFor(s.db.Flavor)
	if err != nil {
		return err
	}
	if err := s.db.MigrateUp(ctx, sub, opts...); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}
	return nil
}

// migrationsFor returns the embedded migrations written for flavor, rooted so
// the filenames the migrator records are the bare "002_admins.up.sql" — the same
// version strings in every dialect, which is what lets a project be pointed at
// a different database without its recorded history changing meaning.
func migrationsFor(flavor sqldb.Flavor) (fs.FS, error) {
	dir, ok := migrationDirs[flavor]
	if !ok {
		return nil, fmt.Errorf("db migrate: no migrations for flavor %s", flavor)
	}
	sub, err := fs.Sub(migrationFS, "migrations/"+dir)
	if err != nil {
		return nil, fmt.Errorf("db migrate: resolve embedded migrations for %s: %w", flavor, err)
	}
	return sub, nil
}

// MigrationsEnabled reports whether this service applies migrations, i.e.
// whether the [db.migrations] section is present.
//
// It exists for other components that carry their own schema: they must not
// migrate behind the back of an operator who left the section out to manage
// schema by hand. Nil-safe, like the accessors on the other services.
func (s *Service) MigrationsEnabled() bool {
	return s.cfg != nil && s.cfg.Migrations != nil
}

// MigrationTable returns the migrations table other components should record
// their own history in, or "" when migrations are disabled.
//
// One table, one row per migration service — that is sqldb's model
// (WithMigrationService), and it is why this is exposed as a string instead of
// each component inventing a table. An empty result means "let sqldb use its
// default", which matches how the section's own `table` key behaves.
func (s *Service) MigrationTable() string {
	if !s.MigrationsEnabled() {
		return ""
	}
	return s.cfg.Migrations.Table
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
