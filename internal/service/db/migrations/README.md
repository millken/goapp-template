# Migrations

One subdirectory per SQL dialect — `sqlite/`, `mysql/`, `postgres/` — holding the
**same numbered files** written in each dialect's own DDL. `internal/service/db`
embeds all three and picks one at `Start` from the driver's flavor
(`migrationsFor` in `db.go`), so the version string recorded in the migrations
table is the bare `002_admins` regardless of which database is behind it.

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
