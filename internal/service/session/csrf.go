package session

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
)

// csrfKey namespaces the token inside the session values, alongside the flash
// keys. The `_` prefix marks it framework-reserved, as flashPrefix does, so it
// cannot collide with an application key.
const csrfKey = "_csrf"

// CSRFFormField and CSRFHeader are where a request may carry the token. The
// field is the one that matters: the PJAX layer submits a form as FormData, so
// a hidden input reaches the server unchanged whether or not JavaScript
// intercepted the submit. The header exists for a future JSON client.
const (
	CSRFFormField = "_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// CSRFToken returns this session's token, minting and persisting one on first
// call. Persisting is the point: the token has to survive to the request that
// submits the form, and saving is also what gives an anonymous visitor a
// session — which is why this is called by handlers that render a form rather
// than by the middleware for everyone.
func (s *session) CSRFToken(ctx context.Context) (string, error) {
	if v, ok := s.values[csrfKey]; ok {
		if token, ok := v.(string); ok && token != "" {
			return token, nil
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: generate csrf token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	s.Set(csrfKey, token)
	if _, err := s.Save(ctx); err != nil {
		return "", fmt.Errorf("session: persist csrf token: %w", err)
	}
	return token, nil
}

// validCSRF reports whether r carries this session's token.
//
// A session that has never minted one rejects everything, including an empty
// submission — matching empty against empty would turn "no token anywhere" into
// a pass, which is exactly the request this exists to refuse.
func (s *session) validCSRF(r *http.Request) bool {
	want, _ := s.values[csrfKey].(string)
	if want == "" {
		return false
	}

	got := r.Header.Get(CSRFHeader)
	if got == "" {
		// ParseForm on a POST reads the body, which the handler then re-reads
		// from the parsed form rather than the stream — the same thing every
		// c.PostForm call already relies on.
		if err := r.ParseForm(); err == nil {
			got = r.PostFormValue(CSRFFormField)
		}
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// Regenerate moves the session to a fresh id, keeping its values and deleting
// the old entry.
//
// Called on sign-in. Without it this package's own CSRF token would introduce
// session fixation: issuing a token on the login page creates a session before
// authentication, and Store.Save keeps an id it is given, so the id an attacker
// could plant would still be valid afterwards.
func (s *session) Regenerate(ctx context.Context) error {
	old := s.id
	// The token belongs to the abandoned id; a form rendered against it is
	// worthless now, so mint a new one on next use rather than carry it over.
	delete(s.values, csrfKey)

	// The old entry goes first, and that order is the whole point. Saving first
	// and deleting after leaves two live states on failure: the caller has a
	// working new session but an error to report, and the planted id it was
	// supposed to invalidate is still valid — the exact window this function
	// exists to close. Deleting first means a failure changes nothing, so the
	// caller can simply refuse. If the save then fails, the worst case is a
	// signed-out user who retries.
	if old != "" {
		if err := s.mod.store.Delete(ctx, old); err != nil {
			return fmt.Errorf("session: regenerate: drop old entry: %w", err)
		}
	}

	s.id = ""
	if _, err := s.Save(ctx); err != nil {
		return fmt.Errorf("session: regenerate: %w", err)
	}
	return nil
}
