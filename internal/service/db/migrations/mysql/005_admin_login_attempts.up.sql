-- 005_admin_login_attempts.up.sql (MySQL)
-- Failed sign-in attempts, counted per client address over a sliding window.
--
-- No counter column on purpose: a window is a count of rows newer than a
-- cutoff, which needs no reset and cannot drift. Rows are deleted as they age
-- out, on write, so the table stays proportional to recent activity rather than
-- to history and needs no scheduled job.
--
-- The index is declared inside CREATE TABLE rather than as a second statement:
-- MySQL has no CREATE INDEX IF NOT EXISTS, and inline keeps this file to one
-- statement. ip is VARCHAR(64) — an IPv6 literal is at most 45 characters, plus
-- room for a zone. `at` is UnixNano (BIGINT), matching the other tables.
CREATE TABLE IF NOT EXISTS admin_login_attempts (
    ip VARCHAR(64) NOT NULL,
    at BIGINT NOT NULL,
    INDEX admin_login_attempts_ip_at (ip, at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
