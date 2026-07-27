-- 005_login_attempts.down.sql
-- Drops the window with the table. Anyone currently throttled is released, which
-- is the right outcome: this data is a rate limit, not a record of anything.
DROP INDEX IF EXISTS login_attempts_ip_at;
DROP TABLE IF EXISTS login_attempts;
