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

// findGroup loads the group of the user with userID. usersTable is interpolated
// (it is configurable) and has already been validated by Admin.Validate against
// ^[A-Za-z_]\w*$; the id itself is parameterised.
func findGroup(ctx context.Context, d *sqldb.DB, usersTable string, userID int64) (*group, error) {
	q := fmt.Sprintf(`SELECT g.superuser, g.permissions
		FROM %s u JOIN user_groups g ON g.id = u.group_id
		WHERE u.id = ?`, usersTable)

	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, errNoGroup
		}
		return nil, fmt.Errorf("admin: find group: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("admin: group %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return &group{Superuser: superuser != 0, Permissions: set}, nil
}
