-- 005_admin_login_attempts.up.sql (PostgreSQL)
-- Failed sign-in attempts, counted per client address over a sliding window.
--
-- No counter column on purpose: a window is a count of rows newer than a
-- cutoff, which needs no reset and cannot drift. Rows are deleted as they age
-- out, on write, so the table stays proportional to recent activity rather than
-- to history and needs no scheduled job.
--
-- ip stays TEXT rather than INET: the throttle compares it for equality with a
-- string the Go code produced, and INET would make that comparison depend on
-- the dialect. `at` is UnixNano (BIGINT), matching the other tables.
CREATE TABLE IF NOT EXISTS admin_login_attempts (
    ip TEXT NOT NULL,
    at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS admin_login_attempts_ip_at
    ON admin_login_attempts (ip, at);
