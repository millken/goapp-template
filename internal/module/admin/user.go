package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
	"golang.org/x/crypto/bcrypt"
)

// errInvalidCredentials is returned by authenticate for both unknown-user and
// wrong-password so callers cannot distinguish the two (and neither can users).
var errInvalidCredentials = errors.New("admin: invalid username or password")

// dummyPasswordHash is compared against when the user is not found, so login
// timing does not leak whether a username exists. It is a bcrypt hash of an
// arbitrary string, computed once at init.
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("goapp-admin-timing-dummy"), bcrypt.DefaultCost)

// User is an admin user row. Only the fields needed for authentication are
// mapped; add more as needed per project.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

// HashPassword returns a bcrypt hash of the plaintext password, suitable for
// storing in the users table. Exported for the `goapp admin create-user`
// command.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("admin: hash password: %w", err)
	}
	return string(h), nil
}

// verifyPassword reports whether password matches the bcrypt hash.
func verifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// findUser loads a user by username from the given (pre-validated) table. It
// returns (nil, nil) when no such user exists.
func findUser(ctx context.Context, d *sqldb.DB, table, username string) (*User, error) {
	q := fmt.Sprintf(`SELECT id, username, password_hash FROM %s WHERE username = ?`, table)
	var u User
	if err := d.QueryRowContext(ctx, q, username).Scan(&u.ID, &u.Username, &u.PasswordHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("admin: find user: %w", err)
	}
	return &u, nil
}

// authenticate verifies username+password against the users table. On success
// it returns the user; on unknown user or bad password it returns
// errInvalidCredentials (with a constant-time dummy compare on the unknown-user
// path so timing does not reveal whether the username exists).
func authenticate(ctx context.Context, d *sqldb.DB, table, username, password string) (*User, error) {
	u, err := findUser(ctx, d, table, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		_ = verifyPassword(string(dummyPasswordHash), password) // burn ~equal time
		return nil, errInvalidCredentials
	}
	if !verifyPassword(u.PasswordHash, password) {
		return nil, errInvalidCredentials
	}
	return u, nil
}
