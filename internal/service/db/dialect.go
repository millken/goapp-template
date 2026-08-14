package db

import (
	"fmt"
	"path"
	"strings"

	"github.com/dnsoa/go/sqldb"
)

// The driver names this service accepts, and the sqldb flavor each maps to.
// sqldb.Open knows a longer list (pq-timeouts, cockroach, nrmysql …); this map
// is the shorter one the template's migrations are written for, and it exists so
// a typo is refused here — with the three valid names in the message — instead of
// surfacing as sqldb's bare "unsupported driver".
var driverFlavors = map[string]sqldb.Flavor{
	"sqlite3": sqldb.SQLite,
	"mysql":   sqldb.MySQL,
	"pgx":     sqldb.PostgreSQL,
}

// migrationDirs maps a flavor to its subdirectory under migrations/. Each
// directory holds the same numbered files written in that dialect's own DDL —
// see migrations/README.md for why they are not one templated set.
var migrationDirs = map[sqldb.Flavor]string{
	sqldb.SQLite:     "sqlite",
	sqldb.MySQL:      "mysql",
	sqldb.PostgreSQL: "postgres",
}

// resolveDriver returns the driver name to open with. An explicit cfg.Driver
// always wins — including when it disagrees with what the DSN looks like, since
// the operator naming a driver knows something the string does not say. An empty
// Driver is inferred from the DSN, and an unrecognisable DSN is an error rather
// than a guess: picking wrong here means opening the wrong database, or opening
// a file named after someone's connection string.
func resolveDriver(driver, dsn string) (string, error) {
	if driver != "" {
		if _, ok := driverFlavors[driver]; !ok {
			return "", fmt.Errorf("db: unsupported driver %q; want sqlite3, mysql or pgx "+
				"(or leave db.driver empty to infer it from db.dsn)", driver)
		}
		return driver, nil
	}
	if d, ok := sniffDriver(dsn); ok {
		return d, nil
	}
	return "", fmt.Errorf("db: cannot infer a driver from dsn %q — set db.driver to "+
		"sqlite3, mysql or pgx explicitly. Recognised shapes are: postgres:// or "+
		"postgresql:// URLs and host=/dbname= keyword strings (pgx); mysql:// URLs "+
		"and user:pass@tcp(host:port)/name DSNs (mysql); file: URLs, :memory:, and "+
		"paths ending .db, .sqlite or .sqlite3 (sqlite3)", dsn)
}

// sniffDriver guesses the driver from the shape of a DSN. The unambiguous forms
// are tested first — a URL scheme names its own database — and the loosest
// heuristics last, so a MySQL DSN carrying an sslmode option cannot be mistaken
// for PostgreSQL's keyword form.
func sniffDriver(dsn string) (string, bool) {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return "", false
	}
	lower := strings.ToLower(s)

	switch {
	case hasScheme(lower, "postgres", "postgresql"):
		return "pgx", true
	case hasScheme(lower, "mysql"):
		return "mysql", true
	case hasScheme(lower, "sqlite", "sqlite3", "file"):
		// "file:" is SQLite's own URI form, which is also how the tests reach a
		// named shared-cache :memory: database.
		return "sqlite3", true
	}

	// go-sql-driver's DSN has no scheme; the network part is what identifies it.
	if strings.Contains(lower, "@tcp(") || strings.Contains(lower, "@unix(") {
		return "mysql", true
	}

	// libpq keyword/value form: "host=... user=... dbname=...". dbname= or
	// sslmode= alone is enough — neither appears in the other two dialects'
	// DSNs — while host= and user= are only taken together, since a
	// hypothetical driver option could borrow either name on its own.
	if strings.Contains(lower, "dbname=") || strings.Contains(lower, "sslmode=") ||
		(strings.Contains(lower, "host=") && strings.Contains(lower, "user=")) {
		return "pgx", true
	}

	// A plain filesystem path, which is what SQLite takes. Only the extensions
	// that mean "this is a SQLite database" count: an extensionless path is
	// left to fail loudly, because creating a file named after a mistyped
	// connection string is worse than refusing to start.
	if s == ":memory:" {
		return "sqlite3", true
	}
	switch strings.ToLower(path.Ext(trimDSNQuery(s))) {
	case ".db", ".sqlite", ".sqlite3":
		return "sqlite3", true
	}
	return "", false
}

// hasScheme reports whether dsn starts with any of the given URL schemes.
func hasScheme(dsn string, schemes ...string) bool {
	for _, s := range schemes {
		if strings.HasPrefix(dsn, s+"://") || strings.HasPrefix(dsn, s+":") {
			return true
		}
	}
	return false
}

// trimDSNQuery drops a "?opts" suffix so path.Ext sees the filename.
func trimDSNQuery(dsn string) string {
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		return dsn[:i]
	}
	return dsn
}

// checkMultiStatements refuses a MySQL migration run whose DSN cannot execute a
// multi-statement file.
//
// The migrator hands each .sql file to the driver as one string, and several of
// them hold more than one statement. MySQL rejects that unless the connection
// was opened with multiStatements=true, and the resulting error ("Error 1064:
// You have an error in your SQL syntax") points at the SQL rather than at the
// DSN, so it is worth naming the real cause before the first migration runs.
//
// It is not enabled silently: multiStatements=true widens what a single
// placeholder-less query can do, which is the operator's call to make, not a
// default to be set behind their back.
func checkMultiStatements(flavor sqldb.Flavor, dsn string) error {
	if flavor != sqldb.MySQL {
		return nil
	}
	if strings.Contains(strings.ToLower(dsn), "multistatements=true") {
		return nil
	}
	return fmt.Errorf("db: MySQL migrations need multiStatements=true in the DSN " +
		"(the migrator execs each .sql file as one string, and some hold several " +
		"statements); append ?multiStatements=true to db.dsn, or drop the " +
		"[db.migrations] section and apply migrations/mysql/*.sql yourself")
}
