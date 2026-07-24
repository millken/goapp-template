// Package driver registers the database drivers used by the template.
//
// sqldb does not depend on any driver; the application must blank-import the
// driver(s) it needs so database/sql can register them. This package centralises
// that for the template's default (SQLite via mattn/go-sqlite3). Swap or add
// drivers here as needed (e.g. _ "github.com/lib/pq", _ "github.com/go-sql-driver/mysql").
package driver

import (
	// SQLite driver (cgo). Registered name: "sqlite3".
	_ "github.com/mattn/go-sqlite3"
)
