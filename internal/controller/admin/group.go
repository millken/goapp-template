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

// statusActive and statusDisabled are the values of users.status (migration 004).
const (
	statusActive   = 1
	statusDisabled = 0
)

// caller is who is making the request: their group, plus the two user-row facts
// the request needs — the username the topbar shows and the status the disabled
// check reads. Bundled into one struct rather than returned as three values
// beside an error, and loaded by one query, because "one group lookup per
// request" is the constraint this whole path is built around.
type caller struct {
	group    *group
	username string
	status   int
	avatar   string
}

// findCaller loads the group, username, status and avatar of the user with
// userID. adminsTable is interpolated (it is configurable) and has already been
// validated by Admin.Validate against ^[A-Za-z_]\w*$; the id itself is
// parameterised.
//
// avatar rides along unconditionally rather than behind a marker: migration 006
// (users.avatar) is itself unmarked, so the column exists in every generated
// project regardless of whether the storage component is present. With storage
// stripped the value is simply always empty and AdminShell renders nothing for
// it — the same reasoning that keeps this query to one row per request.
func findCaller(ctx context.Context, d *sqldb.DB, adminsTable string, userID int64) (*caller, error) {
	q := fmt.Sprintf(`SELECT u.username, u.status, u.avatar, g.superuser, g.permissions
		FROM %s u JOIN admin_groups g ON g.id = u.group_id
		WHERE u.id = ?`, adminsTable)

	var cl caller
	var superuser int
	var raw string
	if err := d.QueryRowContext(ctx, q, userID).Scan(&cl.username, &cl.status, &cl.avatar, &superuser, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The join drops users whose group_id is null or dangling, so this
			// covers "no group" and "unknown user" alike. Both deny.
			return nil, errNoGroup
		}
		return nil, fmt.Errorf("admin: find caller: %w", err)
	}

	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("admin: group of user %d has unreadable permissions: %w", userID, err)
	}
	set := make(permSet, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	cl.group = &group{Superuser: superuser != 0, Permissions: set}
	return &cl, nil
}

// FindGroupID resolves a group name to its id. Exported for
// `myapp admin create-user --group`, which must fail on an unknown name rather
// than leave a user with a null group_id — such a user logs in successfully and
// is then refused everything, which reads as a bug rather than a misconfiguration.
func FindGroupID(ctx context.Context, d *sqldb.DB, name string) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT id FROM admin_groups WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("no permission group named %q — the migration seeds "+
			"'Administrators'; pass --group with an existing name", name)
	}
	if err != nil {
		return 0, fmt.Errorf("admin: look up group %q: %w", name, err)
	}
	return id, nil
}
