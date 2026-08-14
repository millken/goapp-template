-- 003_admin_groups.up.sql (SQLite)
-- Admin permission groups. A group holds a flat JSON array of permission keys
-- ("post.access", "post.modify"); superuser bypasses the check entirely.
--
-- First migration with more than one statement, and the migrator execs each
-- file as a single string — which is why a MySQL DSN needs multiStatements=true.
--
-- This writes the literal `admins` table. The [admin] table setting redirects
-- runtime lookups only (embedded SQL cannot read config), so pointing it
-- elsewhere makes that table's schema the operator's responsibility.
CREATE TABLE IF NOT EXISTS admin_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    superuser   INTEGER NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
);

ALTER TABLE admins ADD COLUMN group_id INTEGER REFERENCES admin_groups(id);

-- Seeded so the first `admin create-user` has somewhere to put an account that
-- can actually reach the admin area. A plain INSERT is safe: the migrator
-- records a version and wraps each file in a transaction, so this runs once.
INSERT INTO admin_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]',
        CAST(strftime('%s', 'now') AS INTEGER) * 1000000000);

-- An admin with no group is refused everything — including the dashboard and
-- logout, because the group lookup runs before the exemption. On a fresh
-- database this matches no rows; it exists so the file is safe to re-run after
-- a rollback that left rows behind.
UPDATE admins
   SET group_id = (SELECT id FROM admin_groups WHERE name = 'Administrators')
 WHERE group_id IS NULL;
