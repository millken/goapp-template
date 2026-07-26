package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dnsoa/go/sqldb"
	"golang.org/x/crypto/bcrypt"
)

// errInvalidCredentials covers both unknown-user and wrong-password so callers
// cannot distinguish the two.
var errInvalidCredentials = errors.New("admin: invalid username or password")

// errAccountDisabled is returned only after the password verified — see
// authenticate.
var errAccountDisabled = errors.New("admin: account is disabled")

// dummyPasswordHash is compared on the unknown-user path so login timing does
// not leak whether a username exists (bcrypt hash of an arbitrary string).
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("goapp-admin-timing-dummy"), bcrypt.DefaultCost)

// User is an admin user row (only the fields needed for auth are mapped).
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Status       int
}

// HashPassword returns a bcrypt hash of the password. Exported for
// `goapp admin create-user`.
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

// findUser loads a user by username, returning (nil, nil) if none exists.
func findUser(ctx context.Context, d *sqldb.DB, table, username string) (*User, error) {
	q := fmt.Sprintf(`SELECT id, username, password_hash, status FROM %s WHERE username = ?`, table)
	var u User
	if err := d.QueryRowContext(ctx, q, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("admin: find user: %w", err)
	}
	return &u, nil
}

// authenticate verifies username+password. On unknown user or bad password it
// returns errInvalidCredentials (with a constant-time dummy compare on the
// unknown-user path so timing does not reveal whether the username exists).
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
	// Only now, with the correct password proven: an earlier check would make a
	// disabled account distinguishable from a wrong password by timing, which is
	// the leak the dummy compare above exists to prevent. Whoever reaches this
	// line holds the credential, so naming the real reason reveals nothing.
	if u.Status == statusDisabled {
		return nil, errAccountDisabled
	}
	return u, nil
}
