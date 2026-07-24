-- 002_users.up.sql
-- Admin users table, backing the admin module's login. Passwords are stored as
-- bcrypt hashes (never plaintext); seed the first user with `goapp admin
-- create-user <username>`.
--
-- SQLite flavor (the template's default driver). For other dialects adjust the
-- id/auto-increment line:
--   PostgreSQL: id BIGSERIAL PRIMARY KEY
--   MySQL:      id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY
-- created_at is UnixNano (BIGINT), matching the sessions table convention.
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    BIGINT NOT NULL
);
