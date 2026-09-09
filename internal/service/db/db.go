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
	"regexp"
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
		_ = d.Close()
		return fmt.Errorf("db ping: %w", err)
	}
	s.db = d

	if mg := s.cfg.Migrations; mg != nil {
		if err := s.migrate(ctx, mg); err != nil {
			_ = d.Close()
			s.db = nil
			return err
		}
	}
	return nil
}

// migrationTableRe is the same shape sqldb itself enforces on the name (its
// migrationTableMatcher). It is re-checked on the precreate path because that
// path concatenates the name into DDL on a connection that MySQL requires to
// run with multiStatements=true: without the re-check, a crafted table name
// from config would execute as more than one statement instead of being
// rejected.
var migrationTableRe = regexp.MustCompile(`^[\w.]+$`)

// precreateMigrationsTable works around dnsoa/go/sqldb v0.0.4's migrations
// table DDL: `service text not null, primary key (service)`. MySQL refuses a
// TEXT column in a key specification (Error 1170), so the migrator's own
// `create table if not exists` fails on the very first run against an empty
// MySQL — the production path for anyone who picks the mysql driver, not just
// CI. Pre-creating the table with a VARCHAR primary key is enough: the
// migrator's IF NOT EXISTS then no-ops and everything downstream (upsert,
// version compare) is type-agnostic. SQLite and PostgreSQL accept the original
// DDL, so the pre-create is MySQL-only. Fixed upstream, this becomes a no-op.
func precreateMigrationsTable(ctx context.Context, d *sqldb.DB, table string) error {
	if d.Flavor != sqldb.MySQL {
		return nil
	}
	if !migrationTableRe.MatchString(table) {
		return fmt.Errorf("db: illegal migration table name %q", table)
	}
	// 191: the safe single-column index length under utf8mb4, the same ceiling
	// the queue's unique_key column uses. version matches service's width so a
	// long migration stem cannot overflow the column the migrator writes its
	// high-water mark into (Error 1406, raised after the DDL has committed —
	// too late to fix cheaply).
	_, err := d.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS `+table+` (
			service VARCHAR(191) NOT NULL,
			version VARCHAR(191) NOT NULL DEFAULT '',
			PRIMARY KEY (service)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	if err != nil {
		return fmt.Errorf("db: precreate migrations table %s: %w", table, err)
	}
	return nil
}

// migrate runs up-migrations using the dialect's slice of the embedded FS.
func (s *Service) migrate(ctx context.Context, mg *Migrations) error {
	if err := checkMultiStatements(s.db.Flavor, s.cfg.DSN); err != nil {
		return err
	}
	// The migrator resolves an absent table name to "migrations" only inside
	// its own option handling; precreate needs the effective name eagerly, so
	// resolve it once here and feed the same value to both paths.
	table := mg.Table
	if table == "" {
		table = "migrations" // sqldb's default, see WithMigrationTable
	}
	if err := precreateMigrationsTable(ctx, s.db, table); err != nil {
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
