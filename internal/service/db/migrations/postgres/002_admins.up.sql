-- 002_admins.up.sql (PostgreSQL)
-- The admin accounts backing the admin module's login. Named `admins`, not
-- `users`, so an application is free to own a `users` table of its own — these
-- rows are operators of the admin area, not the app's end users.
--
-- Passwords are bcrypt hashes, never plaintext; seed the first account with
-- `goapp admin create-user <username>`.
--
-- created_at is UnixNano in a BIGINT, matching every other table here. INTEGER
-- is 32-bit and would not hold it.
CREATE TABLE IF NOT EXISTS admins (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    BIGINT NOT NULL
);
