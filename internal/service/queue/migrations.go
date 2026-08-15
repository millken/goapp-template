package queue

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/dnsoa/go/sqldb"
)

// migrationFS holds this component's own schema, one subdirectory per dialect
// with the same numbered files — the same arrangement as
// internal/service/db/migrations, and the divergence table in that directory's
// README.md applies here too.
//
//go:embed migrations/*/*.sql
var migrationFS embed.FS

// migrationDirs maps a flavor to its subdirectory under migrations/.
var migrationDirs = map[sqldb.Flavor]string{
	sqldb.SQLite:     "sqlite",
	sqldb.MySQL:      "mysql",
	sqldb.PostgreSQL: "postgres",
}

// migrationService is the namespace these migrations are recorded under, and the
// reason the queue carries its own migrations at all rather than adding a
// numbered file to internal/service/db/migrations.
//
// sqldb's migrator does not track a set of applied files: it keeps ONE version
// string per service (migrations table is `(service, version)` keyed by service)
// and skips every file whose version compares <= that high-water mark. Under a
// shared numbering, the queue's file would have to claim the next free number —
// say 007. A project generated without the queue component never applies it and
// its mark moves on to 008, 009, … The day that project adds the queue back,
// `007 <= 009` and the file is silently skipped: no tables, and the failure
// surfaces at runtime as "no such table: queue_tasks".
//
// A separate service name removes the coupling entirely. The queue's history
// starts at "" no matter how far the application's own schema has moved, so
// enabling the component later always works — and internal/service/queue owns
// its schema wholesale, so `goappctl init` strips it by deleting one directory
// and a queue-less project carries no unused tables.
const migrationService = "queue"

// migrate applies the queue's schema. table is the migrations table the db
// component resolved; the queue writes its own row in it rather than its own
// table, because two tables tracking migrations in one database is a worse
// surprise than one table with two rows. An empty table means "sqldb's default",
// matching how the [db.migrations] `table` key behaves when left out.
//
// The table name is passed in rather than read from config: the service container
// deliberately holds no *config.Config (that edge would close an import cycle),
// and the db component is the only thing that knows whether auto-migration is on
// at all. An operator managing schema by hand — no [db.migrations] section — gets
// no queue migration either, which is the same answer the rest of the schema
// gives them.
//
// A free function, not a method: nothing here needs the Service, and keeping it
// free means the schema can be applied by a test with only a handle.
func migrate(ctx context.Context, db *sqldb.DB, table string) error {
	sub, err := migrationsFor(db.Flavor)
	if err != nil {
		return err
	}
	opts := []sqldb.MigrationOption{sqldb.WithMigrationService(migrationService)}
	if table != "" {
		opts = append(opts, sqldb.WithMigrationTable(table))
	}
	if err := db.MigrateUp(ctx, sub, opts...); err != nil {
		return fmt.Errorf("queue: migrate: %w", err)
	}
	return nil
}

// migrationsFor returns the migrations written for flavor, rooted so the version
// the migrator records is the bare "001_queue" in every dialect — which is what
// lets a project be repointed at a different database without its recorded
// history changing meaning.
func migrationsFor(flavor sqldb.Flavor) (fs.FS, error) {
	dir, ok := migrationDirs[flavor]
	if !ok {
		return nil, fmt.Errorf("queue: migrate: no migrations for flavor %s", flavor)
	}
	sub, err := fs.Sub(migrationFS, "migrations/"+dir)
	if err != nil {
		return nil, fmt.Errorf("queue: migrate: resolve embedded migrations for %s: %w", flavor, err)
	}
	return sub, nil
}
