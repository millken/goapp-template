// Package driver blank-imports the database drivers so database/sql registers
// them. All three dialects the template supports are registered here, because
// registration is what makes `db.driver` (or the DSN sniffing in
// internal/service/db) a runtime choice rather than a build-time one: a driver
// that is not linked in cannot be selected by config.
//
// The cost of carrying all three is binary size, not startup work — an unused
// driver registers a name and does nothing else. Drop the imports you do not
// need if the binary size matters more than the freedom to repoint a DSN.
//
// Only go-sqlite3 needs cgo; a CGO_ENABLED=0 build keeps mysql and pgx.
package driver

import (
	// MySQL / MariaDB. Registered name: "mysql".
	_ "github.com/go-sql-driver/mysql"

	// PostgreSQL, via pgx's database/sql shim. Registered name: "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"

	// SQLite (cgo). Registered name: "sqlite3".
	_ "github.com/mattn/go-sqlite3"
)
