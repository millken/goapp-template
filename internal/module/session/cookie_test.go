package session

import (
	"testing"
)

func TestSignAndVerifyCookie(t *testing.T) {
	cases := []string{
		"abc123",
		"",
		"a-session-id-with-dashes-and_underscores",
		"unicode-✨-id",
		string(make([]byte, 64)), // binary-ish
	}
	const secret = "super-secret-key"
	for _, id := range cases {
		signed := signCookie(secret, id)
		got, err := verifyCookie(secret, signed)
		if err != nil {
			t.Errorf("verifyCookie(%q) error: %v", id, err)
			continue
		}
		if got != id {
			t.Errorf("verifyCookie roundtrip: got %q, want %q", got, id)
		}
	}
}

func TestVerifyCookie_Tampered(t *testing.T) {
	const secret = "key"
	signed := signCookie(secret, "id-123")

	// Flip a character in the tag portion.
	tampered := signed[:len(signed)-1]
	if c := tampered[len(tampered)-1]; c == 'a' {
		tampered = tampered[:len(tampered)-1] + "b"
	} else {
		tampered = tampered[:len(tampered)-1] + "a"
	}

	if _, err := verifyCookie(secret, tampered); err == nil {
		t.Error("expected error for tampered cookie, got nil")
	}
}

func TestVerifyCookie_WrongSecret(t *testing.T) {
	signed := signCookie("secret-a", "id")
	if _, err := verifyCookie("secret-b", signed); err == nil {
		t.Error("expected error for wrong secret, got nil")
	}
}

func TestVerifyCookie_Malformed(t *testing.T) {
	cases := []string{
		"",
		"nodothere",
		".leadingdot",
		"trailingdot.",
		"a.b.c",
	}
	for _, raw := range cases {
		if _, err := verifyCookie("k", raw); err == nil {
			t.Errorf("expected error for malformed %q, got nil", raw)
		}
	}
}
