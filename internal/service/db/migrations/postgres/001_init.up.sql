-- 001_init.up.sql (PostgreSQL)
-- Sample table so the migration wiring can be verified end-to-end. Copy or
-- remove per project.
--
-- The column is `name`, not `key`: `key` is a reserved word in MySQL, so a
-- portable query could not spell it without backticks this dialect rejects.
CREATE TABLE IF NOT EXISTS app_meta (
    name  TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
