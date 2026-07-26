package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
	"github.com/millken/inertia"
)

// The two rules an administrator can trip. Both are refusals, not failures:
// callers turn them into a message rather than a 500.
var (
	errSelfTarget    = errors.New("admin: refusing to act on your own account")
	errLastSuperuser = errors.New("admin: this would leave no enabled superuser")
)

// callerID is the signed-in user's id, read from the session the same way
// resolve reads it. Reported false only when there is no usable id, which the
// guarded routes make impossible — resolve has already run.
func (a *Admin) callerID(c *inertia.Context) (int64, bool) {
	v, ok := a.Session.Session(c).Get(a.authKey())
	if !ok {
		return 0, false
	}
	return userID(v)
}

// notSelf refuses an action aimed at the signed-in user. It covers rules 1 and
// 2 — no deleting or disabling yourself, no changing your own group — because
// both reduce to "this target is me".
//
// Enforced here rather than by hiding controls in the UI: the UI is not the
// enforcement point, and a hand-made POST would sail past it.
func (a *Admin) notSelf(c *inertia.Context, targetID int64) error {
	me, ok := a.callerID(c)
	if !ok {
		// No identifiable caller on a route that requires one: refuse rather
		// than allow, since the alternative is acting on someone's behalf
		// without knowing whose.
		return errSelfTarget
	}
	if me == targetID {
		return errSelfTarget
	}
	return nil
}

// keepingASuperuser applies mutate and keeps it only if at least one enabled
// superuser user remains.
//
// The count runs inside the transaction, after the mutation, and that is the
// whole mechanism rather than an implementation detail: it reads the state the
// change actually produced instead of predicting it. Four different changes can
// violate the rule — disabling a user, deleting a user, moving a user out of a
// superuser group, and clearing a group's superuser flag — and a pre-check per
// path is a design where the fifth path someone adds later is a silent hole.
//
// Note for callers: sqldb's Transaction begins with context.Background(), so the
// transaction itself is not cancellable; the queries inside it take ctx, which is
// what matters. And with the pool pinned to one connection (as the tests do for
// :memory:), any query issued on a.DB while this transaction is open will block
// forever — use the tx handle.
func (a *Admin) keepingASuperuser(ctx context.Context, mutate func(tx *sqldb.Tx) error) error {
	return a.DB.Transaction(func(tx *sqldb.Tx) error {
		if err := mutate(tx); err != nil {
			return err
		}
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s u JOIN user_groups g ON g.id = u.group_id
			WHERE g.superuser = 1 AND u.status = ?`, a.usersTable())
		var n int
		if err := tx.QueryRowContext(ctx, q, statusActive).Scan(&n); err != nil {
			return fmt.Errorf("admin: count enabled superusers: %w", err)
		}
		if n == 0 {
			return errLastSuperuser
		}
		return nil
	})
}
