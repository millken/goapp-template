// Package driver blank-imports the database drivers so database/sql registers
// them. The template's default is SQLite (mattn/go-sqlite3); swap or add drivers
// here (e.g. _ "github.com/lib/pq", _ "github.com/go-sql-driver/mysql").
package driver

import (
	// SQLite driver (cgo). Registered name: "sqlite3".
	_ "github.com/mattn/go-sqlite3"
)
