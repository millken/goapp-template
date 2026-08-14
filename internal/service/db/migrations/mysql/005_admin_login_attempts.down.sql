-- 005_admin_login_attempts.down.sql (MySQL)
-- Drops the window with the table, and the inline index with it. Anyone
-- currently throttled is released, which is the right outcome: this data is a
-- rate limit, not a record.
DROP TABLE IF EXISTS admin_login_attempts;
