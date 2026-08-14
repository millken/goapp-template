package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/dnsoa/go/sqldb"
)

// countEnabledSuperusers is the property the guard defends, spelled out here so
// the test does not depend on the guard's own SQL being right.
func countEnabledSuperusers(t *testing.T, adm *Admin) int {
	t.Helper()
	var n int
	if err := adm.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM admins u JOIN admin_groups g ON g.id = u.group_id
		 WHERE g.superuser = 1 AND u.status = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The mutation is rolled back, not merely refused: the row must still be there
// afterwards. A guard that reported an error but left the change applied would
// be worse than none, and only an after-the-fact read can tell the difference.
func TestKeepingASuperuser_RollsBackTheChange(t *testing.T) {
	_, adm := loginStack(t) // alice is in Administrators (superuser), status 1
	ctx := context.Background()
	if before := countEnabledSuperusers(t, adm); before != 1 {
		t.Fatalf("fixture should have exactly 1 enabled superuser, has %d", before)
	}

	err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE admins SET status = 0 WHERE username = 'alice'`)
		return err
	})
	if !errors.Is(err, errLastSuperuser) {
		t.Fatalf("want errLastSuperuser, got %v", err)
	}
	if after := countEnabledSuperusers(t, adm); after != 1 {
		t.Errorf("enabled superusers = %d after the refused change, want 1 — it was not rolled back", after)
	}
}

// The same guard has to cover every path, which is the reason it is one guard.
func TestKeepingASuperuser_CoversAllFourPaths(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		sql  string
	}{
		{"disable the user", `UPDATE admins SET status = 0 WHERE username = 'alice'`},
		{"delete the user", `DELETE FROM admins WHERE username = 'alice'`},
		{"move the user out of the superuser group", `UPDATE admins SET group_id = NULL WHERE username = 'alice'`},
		{"clear the group's superuser flag", `UPDATE admin_groups SET superuser = 0 WHERE name = 'Administrators'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, adm := loginStack(t)
			err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
				_, err := tx.ExecContext(ctx, c.sql)
				return err
			})
			if !errors.Is(err, errLastSuperuser) {
				t.Errorf("want errLastSuperuser, got %v", err)
			}
			if n := countEnabledSuperusers(t, adm); n != 1 {
				t.Errorf("enabled superusers = %d, want 1 (rolled back)", n)
			}
		})
	}
}

// Clearing the flag on a group with several members drops them all at once, so
// the count has to see the staged change rather than predict it.
func TestKeepingASuperuser_AllowsAChangeThatLeavesOne(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	if _, err := adm.DB.ExecContext(ctx,
		`INSERT INTO admins (username, password_hash, created_at, status, group_id)
		 VALUES ('bob', 'x', 0, 1, (SELECT id FROM admin_groups WHERE name = 'Administrators'))`); err != nil {
		t.Fatal(err)
	}

	err := adm.keepingASuperuser(ctx, func(tx *sqldb.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE admins SET status = 0 WHERE username = 'alice'`)
		return err
	})
	if err != nil {
		t.Fatalf("disabling one of two superusers must be allowed: %v", err)
	}
	if n := countEnabledSuperusers(t, adm); n != 1 {
		t.Errorf("enabled superusers = %d, want 1 (bob remains)", n)
	}
}

// A failure inside mutate propagates and rolls back; it must not be reported as
// the last-superuser rule, which would send an operator looking in the wrong
// place.
func TestKeepingASuperuser_PropagatesTheMutationError(t *testing.T) {
	_, adm := loginStack(t)
	sentinel := errors.New("boom")
	err := adm.keepingASuperuser(context.Background(), func(tx *sqldb.Tx) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("want the mutation's own error, got %v", err)
	}
	if errors.Is(err, errLastSuperuser) {
		t.Error("a mutation failure must not be reported as the last-superuser rule")
	}
}
