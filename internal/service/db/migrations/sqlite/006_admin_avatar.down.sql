-- 006_admin_avatar.down.sql (SQLite)
-- Discards every stored avatar path. The files themselves stay in the storage
-- tree — this migration never owned them.
ALTER TABLE admins DROP COLUMN avatar;
