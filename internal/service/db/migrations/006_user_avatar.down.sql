-- 006_user_avatar.down.sql
-- Discards every stored avatar path. The files themselves stay in the storage
-- tree — this migration never owned them.
--
-- DROP COLUMN needs SQLite 3.35+ (mattn/go-sqlite3 bundles 3.53); MySQL and
-- PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN avatar;
