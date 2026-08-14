-- 001_init.up.sql (MySQL)
-- Sample table so the migration wiring can be verified end-to-end. Copy or
-- remove per project.
--
-- The column is `name`, not `key`: `key` is a reserved word here, so a portable
-- query could not spell it without backticks that PostgreSQL rejects.
CREATE TABLE IF NOT EXISTS app_meta (
    name  VARCHAR(191) NOT NULL PRIMARY KEY,
    value TEXT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
