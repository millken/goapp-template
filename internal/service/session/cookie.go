package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// The cookie carries only the signed session ID (data lives in the Store).
// Layout: <base64(id)>.<base64(hmac)> — unforgeable without the secret, no
// encryption (the ID is not secret).

// signCookie returns the signed cookie value for the given session ID.
func signCookie(secret, id string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(id))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(enc))
	tag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + tag
}

var errInvalidCookie = errors.New("session: invalid or tampered cookie")

// verifyCookie validates a signed cookie value and returns the session ID. The
// signature is compared in constant time.
func verifyCookie(secret, raw string) (string, error) {
	enc, tag, ok := strings.Cut(raw, ".")
	if !ok || tag == "" {
		return "", errInvalidCookie
	}

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
