package db

import (
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

func TestResolveDriver_ExplicitWins(t *testing.T) {
	// Deliberately mismatched: a PostgreSQL URL with driver: sqlite3. The
	// operator naming a driver knows something the string does not say, so the
	// sniffer must not second-guess them.
	got, err := resolveDriver("sqlite3", "postgres://u:p@localhost/db")
	if err != nil {
		t.Fatalf("resolveDriver: %v", err)
	}
	if got != "sqlite3" {
		t.Errorf("got %q, want sqlite3 — the DSN overrode an explicit driver", got)
	}
}

func TestResolveDriver_RejectsUnknown(t *testing.T) {
	_, err := resolveDriver("mssql", "whatever")
	if err == nil {
		t.Fatal("expected an error for an unsupported driver")
	}
	// The message has to name the alternatives; "unsupported driver: mssql"
	// alone leaves the reader guessing what this template does support.
	for _, want := range []string{"sqlite3", "mysql", "pgx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestSniffDriver(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		// PostgreSQL URLs and libpq keyword strings.
		{"postgres://u:p@127.0.0.1:5432/myapp?sslmode=disable", "pgx"},
		{"postgresql://u:p@127.0.0.1/myapp", "pgx"},
		{"POSTGRES://u@host/db", "pgx"},
		{"host=127.0.0.1 user=app dbname=myapp", "pgx"},
		{"dbname=myapp", "pgx"},
		{"host=/var/run/postgresql user=app", "pgx"},

		// MySQL: the scheme form, and go-sql-driver's schemeless DSN.
		{"mysql://u:p@127.0.0.1:3306/myapp", "mysql"},
		{"app:secret@tcp(127.0.0.1:3306)/myapp?multiStatements=true", "mysql"},
		{"app:secret@unix(/tmp/mysql.sock)/myapp", "mysql"},

		// SQLite: URI form, the in-memory sentinel, and plain paths.
		{"file:app.db?_journal=WAL", "sqlite3"},
		{"file:mig003?mode=memory&cache=shared", "sqlite3"},
		{":memory:", "sqlite3"},
		{"app.db", "sqlite3"},
		{"/var/lib/myapp/store.sqlite", "sqlite3"},
		{"./data/store.sqlite3", "sqlite3"},
		{"store.DB", "sqlite3"},
	}
	for _, c := range cases {
		got, ok := sniffDriver(c.dsn)
		if !ok {
			t.Errorf("sniffDriver(%q): not recognised, want %s", c.dsn, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("sniffDriver(%q) = %s, want %s", c.dsn, got, c.want)
		}
	}
}

// A MySQL DSN can carry options whose names look like libpq keywords. Ordering
// the checks so the network part is tested first is what keeps that from being
// read as PostgreSQL, and it is the one ordering dependency in sniffDriver.
func TestSniffDriver_MySQLDSNWithKeywordLookingOption(t *testing.T) {
	got, ok := sniffDriver("app:secret@tcp(db:3306)/myapp?tls=true&dbname=ignored")
	if !ok || got != "mysql" {
		t.Errorf("got (%q, %v), want (mysql, true)", got, ok)
	}
}

func TestSniffDriver_Unrecognised(t *testing.T) {
	// An extensionless path is the case worth refusing: guessing sqlite3 here
	// would create a file named after whatever the operator actually meant.
	for _, dsn := range []string{"", "   ", "myapp", "/var/lib/myapp/store", "localhost:5432"} {
		if got, ok := sniffDriver(dsn); ok {
			t.Errorf("sniffDriver(%q) = %s, want no guess", dsn, got)
		}
	}
}

func TestResolveDriver_UnrecognisableDSNNamesTheShapes(t *testing.T) {
	_, err := resolveDriver("", "/var/lib/myapp/store")
	if err == nil {
		t.Fatal("expected an error for an unrecognisable DSN")
	}
	if !strings.Contains(err.Error(), "db.driver") {
		t.Errorf("error does not say how to fix it: %v", err)
	}
}

func TestCheckMultiStatements(t *testing.T) {
	// Only MySQL has the switch; the other two must never be refused for it.
	for _, f := range []sqldb.Flavor{sqldb.SQLite, sqldb.PostgreSQL} {
		if err := checkMultiStatements(f, "anything"); err != nil {
			t.Errorf("checkMultiStatements(%s): %v", f, err)
		}
	}
	if err := checkMultiStatements(sqldb.MySQL, "app:p@tcp(h:3306)/db"); err == nil {
		t.Error("MySQL without multiStatements=true was allowed to migrate")
	}
	if err := checkMultiStatements(sqldb.MySQL,
		"app:p@tcp(h:3306)/db?multiStatements=true"); err != nil {
		t.Errorf("MySQL with multiStatements=true refused: %v", err)
	}
	// The DSN is matched case-insensitively; go-sql-driver accepts the option
	// name in any case, so refusing on spelling would be a false alarm.
	if err := checkMultiStatements(sqldb.MySQL,
		"app:p@tcp(h:3306)/db?MultiStatements=True"); err != nil {
		t.Errorf("MySQL with MultiStatements=True refused: %v", err)
	}
}

// The trap the three-directory layout invites: a migration written for one
// dialect and forgotten in another. A project passes its tests on SQLite and
// fails the first time someone repoints the DSN, so the parity check belongs
// here rather than in a review checklist.
func TestMigrationDirs_HaveIdenticalFilenames(t *testing.T) {
	var reference []string
	var referenceDir string

	for _, flavor := range []sqldb.Flavor{sqldb.SQLite, sqldb.MySQL, sqldb.PostgreSQL} {
		sub, err := migrationsFor(flavor)
		if err != nil {
			t.Fatalf("migrationsFor(%s): %v", flavor, err)
		}
		entries, err := fs.ReadDir(sub, ".")
		if err != nil {
			t.Fatalf("read %s migrations: %v", flavor, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)

		if len(names) == 0 {
			t.Fatalf("%s has no migrations", flavor)
		}
		if reference == nil {
			reference, referenceDir = names, flavor.String()
			continue
		}
		if !slices.Equal(names, reference) {
			t.Errorf("%s migrations differ from %s:\n %s: %v\n %s: %v",
				flavor, referenceDir, flavor, names, referenceDir, reference)
		}
	}

	// Every up needs its down: MigrateTo walks the down files, and a missing one
	// silently stops the rollback at the wrong version rather than failing.
	for _, name := range reference {
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		down := strings.TrimSuffix(name, ".up.sql") + ".down.sql"
		if !slices.Contains(reference, down) {
			t.Errorf("%s has no matching %s", name, down)
		}
	}
}

func TestMigrationsFor_UnknownFlavor(t *testing.T) {
	if _, err := migrationsFor(sqldb.Flavor(0)); err == nil {
		t.Fatal("expected an error for an unknown flavor")
	}
}
