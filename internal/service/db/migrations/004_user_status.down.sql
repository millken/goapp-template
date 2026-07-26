-- 004_user_status.down.sql
-- Discards every disabled flag: after rolling back, previously disabled users
-- are indistinguishable from active ones, and re-applying the up migration
-- brings them all back as active. Dump users.status first if you intend to
-- come back.
--
-- DROP COLUMN needs SQLite 3.35+ (mattn/go-sqlite3 bundles 3.53); MySQL and
-- PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN status;
