-- 003_user_groups.up.sql
-- Admin permission groups. A group holds a flat JSON array of permission keys
-- ("post.access", "post.modify"); superuser bypasses the check entirely.
--
-- SQLite flavor (the template's default driver). For other dialects adjust the
-- id/auto-increment line:
--   PostgreSQL: id BIGSERIAL PRIMARY KEY
--   MySQL:      id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY
-- created_at is UnixNano (BIGINT), matching the users and sessions convention;
-- hence the strftime multiplication in the seed below.
--
-- This migration writes the literal `users` table. The [admin] users_table
-- setting redirects runtime lookups only — embedded SQL cannot read config — so
-- pointing it elsewhere makes that table's schema the operator's responsibility.
CREATE TABLE IF NOT EXISTS user_groups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    superuser   INTEGER NOT NULL DEFAULT 0,
    permissions TEXT NOT NULL DEFAULT '[]',
    created_at  BIGINT NOT NULL
);

ALTER TABLE users ADD COLUMN group_id INTEGER REFERENCES user_groups(id);

-- Seeded so the first `admin create-user` has somewhere to put a user that can
-- actually reach the admin area. A plain INSERT is safe: the migrator records a
-- version and wraps each file in a transaction, so this runs exactly once.
INSERT INTO user_groups (name, superuser, permissions, created_at)
VALUES ('Administrators', 1, '[]',
        CAST(strftime('%s', 'now') AS INTEGER) * 1000000000);
