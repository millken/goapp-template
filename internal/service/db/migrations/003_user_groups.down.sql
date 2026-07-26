-- 003_user_groups.down.sql
-- The column goes before the table it references. DROP COLUMN needs SQLite 3.35+
-- (mattn/go-sqlite3 bundles 3.53); MySQL and PostgreSQL support it unconditionally.
ALTER TABLE users DROP COLUMN group_id;
DROP TABLE IF EXISTS user_groups;
