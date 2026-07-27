package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newCSRFSession returns a session backed by the memory store, with a recorder
// standing in for the response writer the middleware normally injects.
func newCSRFSession(t *testing.T) (*session, *Service) {
	t.Helper()
	svc := New(&Config{Secret: "test-secret", Store: StoreMemory}, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return &session{values: map[string]any{}, mod: svc, w: httptest.NewRecorder()}, svc
}

func TestCSRFToken_StableWithinASession(t *testing.T) {
	s, _ := newCSRFSession(t)
	ctx := context.Background()

	first, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if len(first) < 32 {
		t.Errorf("token is %d chars, want something unguessable", len(first))
	}
	second, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if second != first {
		t.Error("a second call minted a new token; a form rendered earlier would stop working")
	}
}

// The first call is what brings a session into existence for an anonymous
// visitor — that is the whole reason the token is created on demand rather than
// for everyone.
func TestCSRFToken_PersistsTheSession(t *testing.T) {
	s, _ := newCSRFSession(t)
	if s.ID() != "" {
		t.Fatal("fixture should start unsaved")
	}
	if _, err := s.CSRFToken(context.Background()); err != nil {
		t.Fatalf("CSRFToken: %v", err)
	}
	if s.ID() == "" {
		t.Error("the token was not persisted, so it will not survive to the next request")
	}
}

func TestValidCSRF(t *testing.T) {
	s, _ := newCSRFSession(t)
	token, err := s.CSRFToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	form := func(v string) *http.Request {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(url.Values{CSRFFormField: {v}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	if !s.validCSRF(form(token)) {
		t.Error("the session's own token was rejected")
	}
	if s.validCSRF(form("")) {
		t.Error("an empty token was accepted")
	}
	if s.validCSRF(form(token + "x")) {
		t.Error("a wrong token was accepted")
	}

	// The header form, for a future JSON client.
	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set(CSRFHeader, token)
	if !s.validCSRF(r) {
		t.Error("the header form was rejected")
	}

	// A session that never minted a token has nothing to match, and must not
	// accept an empty submission by matching empty against empty.
	fresh, _ := newCSRFSession(t)
	if fresh.validCSRF(form("")) {
		t.Error("a session with no token accepted an empty one")
	}
}

// Without this, issuing a token on the login page would leave the pre-login id
// in place after sign-in — session fixation, introduced by the CSRF feature
// rather than fixed by it.
func TestRegenerate_NewIDSameValuesOldEntryGone(t *testing.T) {
	s, svc := newCSRFSession(t)
	ctx := context.Background()
	s.Set("keep", "me")
	old, err := s.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Regenerate(ctx); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if s.ID() == old || s.ID() == "" {
		t.Errorf("id = %q, want a new non-empty id (was %q)", s.ID(), old)
	}
	if v, _ := s.Get("keep"); v != "me" {
		t.Error("values did not survive regeneration")
	}
	if _, _, ok, err := svc.store.Load(ctx, old); err != nil || ok {
		t.Error("the old entry is still in the store; the abandoned cookie still works")
	}
}

// A token tied to the abandoned id is worthless, so it goes with it.
func TestRegenerate_RotatesTheToken(t *testing.T) {
	s, _ := newCSRFSession(t)
	ctx := context.Background()
	before, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Regenerate(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.CSRFToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Error("the token survived regeneration")
	}
}

// The order inside Regenerate is load-bearing. If it saved the new session
// before dropping the old entry, a failed delete would leave the caller holding
// a working session it has to report an error for — and the planted id it was
// meant to invalidate still valid. Deleting first means a failure changes
// nothing.
func TestRegenerate_FailureLeavesTheOldSessionIntact(t *testing.T) {
	s, svc := newCSRFSession(t)
	ctx := context.Background()
	s.Set("keep", "me")
	old, err := s.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	svc.store = failingDelete{svc.store}
	if err := s.Regenerate(ctx); err == nil {
		t.Fatal("Regenerate reported success though the old entry could not be dropped")
	}
	if s.ID() != old {
		t.Errorf("id = %q, want the original %q — a failed regeneration must not move the session", s.ID(), old)
	}
	svc.store = failingDelete{svc.store}.Store
	if _, _, ok, err := svc.store.Load(ctx, old); err != nil || !ok {
		t.Error("the original session is gone, so the caller's refusal logs the user out anyway")
	}
}

// failingDelete is a Store whose Delete always fails; everything else passes
// through.
type failingDelete struct{ Store }

func (failingDelete) Delete(context.Context, string) error {
	return errors.New("store is unavailable")
}
