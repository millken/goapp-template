-- 003_admin_groups.down.sql (PostgreSQL)
-- The column goes before the table it references; the foreign key constraint is
-- dropped along with the column that carries it.
--
-- This discards every group definition, permission set, and group assignment,
-- with no way to recover them. Dump admin_groups and admins.group_id first if
-- you intend to come back.
ALTER TABLE admins DROP COLUMN group_id;
DROP TABLE IF EXISTS admin_groups;
