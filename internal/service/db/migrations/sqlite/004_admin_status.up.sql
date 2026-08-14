-- 004_admin_status.up.sql (SQLite)
-- Whether an admin may sign in and hold a session. 1 = active, 0 = disabled.
--
-- No backfill: a constant DEFAULT on ADD COLUMN fills existing rows, so any row
-- that predates this lands active.
ALTER TABLE admins ADD COLUMN status INTEGER NOT NULL DEFAULT 1;
