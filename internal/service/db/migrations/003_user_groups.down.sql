-- 003_user_groups.down.sql
-- The column goes before the table it references. DROP COLUMN needs SQLite 3.35+
-- (mattn/go-sqlite3 bundles 3.53); MySQL and PostgreSQL support it unconditionally.
--
-- This discards every group definition and permission set, and every user's group
-- assignment, with no way to recover them. Re-applying the up migration puts all
-- users back in Administrators — which is right for an upgrade from before groups
-- existed, but is a privilege grant if you had non-superuser groups. Dump
-- user_groups and users.group_id first if you intend to come back.
ALTER TABLE users DROP COLUMN group_id;
DROP TABLE IF EXISTS user_groups;
