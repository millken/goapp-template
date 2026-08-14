-- 003_admin_groups.down.sql (SQLite)
-- The column goes before the table it references. DROP COLUMN needs SQLite
-- 3.35+ (mattn/go-sqlite3 bundles 3.53).
--
-- This discards every group definition, permission set, and group assignment,
-- with no way to recover them. Dump admin_groups and admins.group_id first if
-- you intend to come back.
ALTER TABLE admins DROP COLUMN group_id;
DROP TABLE IF EXISTS admin_groups;
