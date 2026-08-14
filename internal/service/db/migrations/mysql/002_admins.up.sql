-- 002_admins.up.sql (MySQL)
-- The admin accounts backing the admin module's login. Named `admins`, not
-- `users`, so an application is free to own a `users` table of its own — these
-- rows are operators of the admin area, not the app's end users.
--
-- Passwords are bcrypt hashes, never plaintext; seed the first account with
-- `goapp admin create-user <username>`.
--
-- VARCHAR, not TEXT, for username: MySQL cannot put a UNIQUE index on a TEXT
-- column without a prefix length. 64 is the same ceiling validate.MaxLen(64)
-- applies in Go, and usernameRe is ASCII-only so runes and bytes agree.
-- password_hash is 60 bytes for bcrypt; 255 leaves room for a longer scheme.
--
-- created_at is UnixNano in a BIGINT. INTEGER is 32-bit here and would not hold it.
CREATE TABLE IF NOT EXISTS admins (
    id            BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at    BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
