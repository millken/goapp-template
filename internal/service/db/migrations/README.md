# Migrations

One subdirectory per SQL dialect — `sqlite/`, `mysql/`, `postgres/` — holding the
**same numbered files** written in each dialect's own DDL. `internal/service/db`
embeds all three and picks one at `Start` from the driver's flavor
(`migrationsFor` in `db.go`), so the version string recorded in the migrations
table is the bare `002_admins` regardless of which database is behind it.

<!--goappctl:queue-->
This is the *application's* schema. It is not the only one: a component may carry
its own — see "A component with its own migrations" at the bottom, which is where
`queue_tasks` and friends come from.
<!--goappctl:end-->

## Why three copies rather than one templated set

Because these files stay executable SQL. You can paste `postgres/003_admin_groups.up.sql`
into `psql` to see what it does, and a reviewer reading `mysql/002_admins.up.sql`
sees the statement the server will actually receive. A single set with
`{{.PKAutoInc}}` placeholders would trade that for a smaller diff, and the
divergences below are not the kind that a few placeholders would cover anyway:

| | sqlite | mysql | postgres |
|---|---|---|---|
| auto-increment pk | `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY` | `BIGSERIAL PRIMARY KEY` |
| indexed string | `TEXT` | `VARCHAR(n)` — MySQL cannot index `TEXT` without a prefix length | `TEXT` |
| defaulted string | `TEXT DEFAULT ''` | `VARCHAR(n) DEFAULT ''` — MySQL has no constant `DEFAULT` on `TEXT` | `TEXT DEFAULT ''` |
| UnixNano "now" | `CAST(strftime('%s','now') AS INTEGER) * 1000000000` | `UNIX_TIMESTAMP() * 1000000000` | `EXTRACT(EPOCH FROM now())::bigint * 1000000000` |
| foreign key on `ADD COLUMN` | inline `REFERENCES` | needs a separate `ADD CONSTRAINT` (inline `REFERENCES` is parsed and ignored) | inline `REFERENCES` |
| `CREATE INDEX IF NOT EXISTS` | yes | **no** — the MySQL files declare indexes inside `CREATE TABLE` instead | yes |
| uniquely indexed string | `TEXT` | `VARCHAR(191)` — utf8mb4 puts a 768-byte ceiling on a single-column unique index, and 191 is the longest safe length under it | `TEXT` |
| nullable unique column | many `NULL`s allowed | same | same — so "no key" must be bound as `NULL`, never `''`: every dialect rejects a second empty string |

The rule when adding a migration: **write it in all three directories under the
same filename.** A file present in one dialect and missing in another is a
project that works until someone repoints the DSN.

## Keeping the schemas interchangeable

The column *names and shapes* are identical across the three; only the physical
types differ. That is what lets the Go code hold one set of queries — `?`
placeholders, rewritten to `$1` for PostgreSQL by `dnsoa/go/sqldb` — instead of
one set per dialect. Two consequences worth keeping in mind:

- **No `TEXT` column carries a constant `DEFAULT`.** Where a default is wanted,
  MySQL uses `VARCHAR(n)`, so the length is a real ceiling on that dialect.
- **Timestamps are `BIGINT` UnixNano everywhere**, never a native date type.
  `INTEGER` is 32-bit on MySQL and PostgreSQL and would overflow in 1970 + 2.1s.

## MySQL needs `multiStatements=true`

The migrator hands each `.sql` file to the driver as a single string, and some
hold several statements. MySQL rejects that unless the DSN says
`?multiStatements=true`; `db.Start` refuses to run migrations without it rather
than letting the failure surface as a syntax error. The other two dialects have
no such switch.

## Down migrations

Every up has a down, and `TestMigration…_RollsBackCleanly` exercises the pair on
SQLite. The MySQL and PostgreSQL versions are not covered by the test suite (it
runs against `:memory:` SQLite only) — treat them as reviewed, not proven, and
try them against a scratch database before relying on one in production.

<!--goappctl:queue-->
## A component with its own migrations

An optional component that owns tables keeps them in its own directory, laid out
exactly like this one, and applies them under its **own migration service name**.
`internal/service/queue/migrations/` is the working example:

```go
db.MigrateUp(ctx, sub,
    sqldb.WithMigrationTable(table),      // the same table this component uses
    sqldb.WithMigrationService("queue"))  // but its own row in it
```

The migrations table is keyed by service, so that produces one row per owner:

```
service   version
default   006_admin_avatar
queue     001_queue
```

### Why not just add `007_queue` here

Because `sqldb`'s migrator does not track a *set* of applied files. It keeps one
version string per service and skips every file whose version sorts `<=` it. Under
a shared numbering:

1. the queue's file claims `007`;
2. a project generated *without* the queue never applies it, and its mark moves on
   to `008`, `009`, …;
3. the day that project adds the queue back, `007 <= 009`, so the file is silently
   skipped. No tables, and the first thing anyone sees is
   `no such table: queue_tasks` at runtime.

A separate service name removes the coupling: the component's history starts at
`""` no matter how far the application's own schema has moved, so enabling it later
always works. It also means `goappctl init` strips the schema by deleting one
directory, and a project without the component carries no unused tables.

### The rules still apply, per directory

Same filenames across all three dialects, every up with a down, everything above
about dialect divergence. Each such directory needs its own parity test —
`internal/service/db/dialect_test.go`'s `TestMigrationDirs_HaveIdenticalFilenames`
cannot see anything outside this one, so the queue carries a copy in
`internal/service/queue/migrations_test.go`.

### Numbering restarts

A component's history is its own, so its first file is `001`, not the next free
number here. Nothing compares the two.
<!--goappctl:end-->
