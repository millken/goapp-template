-- 005_admin_login_attempts.down.sql (SQLite)
-- Drops the window with the table. Anyone currently throttled is released,
-- which is the right outcome: this data is a rate limit, not a record.
DROP INDEX IF EXISTS admin_login_attempts_ip_at;
DROP TABLE IF EXISTS admin_login_attempts;
