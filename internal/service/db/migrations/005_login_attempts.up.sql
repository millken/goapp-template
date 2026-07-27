-- 005_login_attempts.up.sql
-- Failed sign-in attempts, counted per client address over a sliding window.
--
-- No counter column on purpose: a window is a count of rows newer than a cutoff,
-- which needs no reset and cannot drift. Rows are deleted as they age out, on
-- write, so the table stays proportional to recent activity rather than to
-- history and needs no scheduled job.
--
-- SQLite flavor (the template's default driver). PostgreSQL and MySQL accept
-- this as written. `at` is UnixNano (BIGINT), matching the other tables.
CREATE TABLE IF NOT EXISTS login_attempts (
    ip TEXT NOT NULL,
    at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS login_attempts_ip_at ON login_attempts (ip, at);
