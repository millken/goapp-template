-- 003_admin_groups.up.sql (PostgreSQL)
-- Admin permission groups. A group holds a flat JSON array of permission keys
-- ("post.access", "post.modify"); superuser bypasses the check entirely.
--
-- permissions stays TEXT rather than JSONB: the Go code round-trips it as a
-- string through encoding/json and never queries inside it, and TEXT is the
-- shape the other two dialects use.
--
-- This writes the literal `admins` table. The [admin] table setting redirects
-- runtime lookups only (embedded SQL cannot read config), so pointing it
-- elsewhere makes that table's schema the operator's responsibility.
CREATE TABLE IF NOT EXISTS admin_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    superuser   SMALLINT NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
);

ALTER TABLE admins ADD COLUMN group_id BIGINT REFERENCES admin_groups(id);

-- Seeded so the first `admin create-user` has somewhere to put an account that
-- can actually reach the admin area. A plain INSERT is safe: the migrator
-- records a version and wraps each file in a transaction, so this runs once.
INSERT INTO admin_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]',
        EXTRACT(EPOCH FROM now())::bigint * 1000000000);

-- An admin with no group is refused everything — including the dashboard and
-- logout, because the group lookup runs before the exemption. On a fresh
-- database this matches no rows; it exists so the file is safe to re-run after
-- a rollback that left rows behind.
UPDATE admins
   SET group_id = (SELECT id FROM admin_groups WHERE name = 'Administrators')
 WHERE group_id IS NULL;
