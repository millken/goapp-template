package admin

import (
	"context"
	"log/slog"
	"time"
)

// The window and its limit. A count over a window rather than a stored counter:
// there is nothing to reset and nothing can drift out of step with reality.
const (
	loginWindow      = 15 * time.Minute
	loginMaxAttempts = 10
)

// loginBlocked reports whether ip has spent its attempts, and how long until the
// oldest of them ages out.
//
// Fails open. This is a rate limit, not an authorisation decision: refusing on a
// storage error would turn a database blip into a total lockout of the admin
// area, which is a far worse outcome than one unthrottled attempt.
func (a *Admin) loginBlocked(ctx context.Context, ip string) (bool, time.Duration) {
	cutoff := time.Now().Add(-loginWindow).UnixNano()
	var n int
	var oldest *int64
	if err := a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(at) FROM admin_login_attempts WHERE ip = ? AND at > ?`,
		ip, cutoff).Scan(&n, &oldest); err != nil {
		slog.Error("admin: count login attempts", "err", err, "ip", ip)
		return false, 0
	}
	if n < loginMaxAttempts || oldest == nil {
		return false, 0
	}
	retry := time.Until(time.Unix(0, *oldest).Add(loginWindow))
	if retry < time.Second {
		retry = time.Second
	}
	return true, retry
}

// recordLoginFailure adds one attempt and drops the ones that have aged out, so
// the table tracks recent activity rather than history and needs no scheduled
// job to stay small.
func (a *Admin) recordLoginFailure(ctx context.Context, ip string) {
	now := time.Now()
	if _, err := a.DB.ExecContext(ctx,
		`INSERT INTO admin_login_attempts (ip, at) VALUES (?, ?)`, ip, now.UnixNano()); err != nil {
		slog.Error("admin: record login failure", "err", err, "ip", ip)
		return
	}
	if _, err := a.DB.ExecContext(ctx,
		`DELETE FROM admin_login_attempts WHERE at <= ?`, now.Add(-loginWindow).UnixNano()); err != nil {
		slog.Warn("admin: prune login attempts", "err", err)
	}
}

// clearLoginFailures forgets an address after it signs in successfully.
func (a *Admin) clearLoginFailures(ctx context.Context, ip string) {
	if _, err := a.DB.ExecContext(ctx, `DELETE FROM admin_login_attempts WHERE ip = ?`, ip); err != nil {
		slog.Warn("admin: clear login attempts", "err", err, "ip", ip)
	}
}
