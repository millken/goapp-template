-- 003_admin_groups.up.sql (MySQL)
-- Admin permission groups. A group holds a flat JSON array of permission keys
-- ("post.access", "post.modify"); superuser bypasses the check entirely.
--
-- First migration with more than one statement, and the migrator execs each
-- file as a single string — which is why this dialect's DSN needs
-- multiStatements=true. db.Start refuses to migrate without it.
--
-- permissions is VARCHAR, not TEXT, only so it can carry a constant DEFAULT:
-- MySQL allows no constant default on a TEXT column. 4096 bytes is the real
-- ceiling on this dialect — roughly 180 permission keys.
--
-- group_id gets its foreign key from a separate ADD CONSTRAINT. An inline
-- REFERENCES on ADD COLUMN is parsed and silently ignored here, which would
-- leave the other two dialects enforcing a constraint this one does not.
--
-- This writes the literal `admins` table. The [admin] table setting redirects
-- runtime lookups only (embedded SQL cannot read config), so pointing it
-- elsewhere makes that table's schema the operator's responsibility.
CREATE TABLE IF NOT EXISTS admin_groups (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(64) NOT NULL UNIQUE,
    superuser   SMALLINT NOT NULL DEFAULT 0,
    permissions VARCHAR(4096) NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE admins
    ADD COLUMN group_id BIGINT NULL,
    ADD CONSTRAINT admins_group_id_fk
        FOREIGN KEY (group_id) REFERENCES admin_groups (id);

-- Seeded so the first `admin create-user` has somewhere to put an account that
-- can actually reach the admin area. A plain INSERT is safe: the migrator
-- records a version and wraps each file in a transaction, so this runs once.
INSERT INTO admin_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]', UNIX_TIMESTAMP() * 1000000000);

-- An admin with no group is refused everything — including the dashboard and
-- logout, because the group lookup runs before the exemption. On a fresh
-- database this matches no rows; it exists so the file is safe to re-run after
-- a rollback that left rows behind. The subquery reads a different table than
-- the UPDATE targets, so MySQL's error 1093 does not apply.
UPDATE admins
   SET group_id = (SELECT id FROM admin_groups WHERE name = 'Administrators')
 WHERE group_id IS NULL;
