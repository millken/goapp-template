-- 003_admin_groups.down.sql (MySQL)
-- The foreign key goes before the column, and the column before the table it
-- referenced: MySQL will not drop a column an active constraint names.
--
-- This discards every group definition, permission set, and group assignment,
-- with no way to recover them. Dump admin_groups and admins.group_id first if
-- you intend to come back.
ALTER TABLE admins
    DROP FOREIGN KEY admins_group_id_fk,
    DROP COLUMN group_id;
DROP TABLE IF EXISTS admin_groups;
