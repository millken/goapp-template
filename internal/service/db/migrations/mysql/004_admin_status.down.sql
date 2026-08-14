-- 004_admin_status.down.sql (MySQL)
-- Discards every disabled flag: after rolling back, previously disabled admins
-- are indistinguishable from active ones. Dump admins.status first if you
-- intend to come back.
ALTER TABLE admins DROP COLUMN status;
