-- 001_init.up.sql
-- Sample migration for the db module template. Creates a trivial table so the
-- migration wiring can be verified end-to-end. Copy/remove as needed per project.
CREATE TABLE IF NOT EXISTS app_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
