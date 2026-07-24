package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// signedValue is a tamper-proof carrier for the session ID in a cookie. It
// base64-encodes the ID and appends an HMAC-SHA256 tag over the encoded bytes,
// so a client cannot forge or alter another session's ID without the secret.
//
// Layout: <base64(id)>.<base64(hmac)>
//
// This is intentionally minimal: the cookie carries only the (signed) session
// ID; the session data lives in the Store. Encryption is not provided — the ID
// is not secret, it only needs to be unforgeable.

// signCookie returns the signed cookie value for the given session ID.
func signCookie(secret, id string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(id))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(enc))
	tag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + tag
}

// verifyCookie validates a signed cookie value produced by signCookie and
// returns the session ID. It returns an error if the value is malformed or the
// signature does not match (constant-time comparison).
var errInvalidCookie = errors.New("session: invalid or tampered cookie")

func verifyCookie(secret, raw string) (string, error) {
	dot := -1
	for i, b := range []byte(raw) {
		if b == '.' {
			dot = i
			break
		}
	}
	if dot < 0 || dot == len(raw)-1 {
		return "", errInvalidCookie
	}
	enc := raw[:dot]
	tag := raw[dot+1:]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(enc))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(tag)) {
		return "", errInvalidCookie
	}

	id, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", errInvalidCookie
	}
	return string(id), nil
}
