package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
)

// errNoGroup means the user exists but has no group — or no longer does, if a
// group was deleted from under them. Distinct from a database failure so the
// caller can answer 403 for one and 500 for the other.
var errNoGroup = errors.New("admin: user has no group")

// group is the authorisation state for one signed-in user.
type group struct {
	Superuser   bool
	Permissions permSet
}

// findGroup loads the group and username of the user with userID. usersTable is
// interpolated (it is configurable) and has already been validated by
// Admin.Validate against ^[A-Za-z_]\w*$; the id itself is parameterised. The
// username rides along because the topbar shows it, and adding a second query
// for one column would break the one-lookup-per-request rule.
func findGroup(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*group, string, error) {
	q := fmt.Sprintf(`SELECT u.username, g.superuser, g.permissions
		FROM %s u JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, usersTable)

	var username string
	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&username, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, "", errNoGroup
		}
		return nil, "", fmt.Errorf("admin: find group: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, "", fmt.Errorf("admin: group %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return &group{Superuser: superuser != 0, Permissions: set}, username, nil
}

// FindGroupID resolves a group name to its id. Exported for
// `myapp admin create-user --group`, which must fail on an unknown name rather
// than leave a user with a null group_id — such a user logs in successfully and
// is then refused everything, which reads as a bug rather than a misconfiguration.
func FindGroupID(ctx context.Context, d *sqldb.DB, name string) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT id FROM user_groups WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no permission group named %q — the migration seeds "+
			"'Administrators'; pass --group with an existing name", name)
	}
	if err != nil {
		return 0, fmt.Errorf("admin: look up group %q: %w", name, err)
	}
	return id, nil
}
