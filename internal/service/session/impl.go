package session

import (
	"context"
	"net/http"
)

// session is the concrete Session implementation. It is created per request by
// the middleware, holds a snapshot of the store data, and writes back on Save.
//
// The cookie is written synchronously in Save/Destroy (not after the handler
// chain): inertia's ResponseWriter is write-through — the first body write
// flushes the header block — so a cookie set after the handler renders would be
// dropped. The middleware injects the request's ResponseWriter (w) so Save can
// emit Set-Cookie before the handler writes its body. Consumers only call
// Save/Destroy; they never touch the response directly.
type session struct {
	id     string
	values map[string]any
	mod    *Service
	w      http.ResponseWriter // injected by the middleware; nil only if misused outside a request
}

func (s *session) ID() string { return s.id }

func (s *session) Get(key string) (any, bool) {
	v, ok := s.values[key]
	return v, ok
}

func (s *session) Set(key string, value any) {
	if s.values == nil {
		s.values = make(map[string]any)
	}
	s.values[key] = value
}

func (s *session) Delete(key string) {
	delete(s.values, key)
}

// Save persists the session to the store and writes the signed cookie on the
// response. If the session is new (no id) the store assigns one. Call it before
// the handler writes any body (the conventional order: mutate → Save → render),
// so the Set-Cookie header lands before the response is flushed.
func (s *session) Save(ctx context.Context) (string, error) {
	id, err := s.mod.store.Save(ctx, s.id, s.values, s.mod.ttl())
	if err != nil {
		return "", err
	}
	s.id = id
	s.mod.setCookie(s.w, id)
	return id, nil
}

// Destroy removes the session from the store and clears the response cookie.
// The cookie is cleared even if the session was never persisted (no id), so a
// half-built session cannot leave a stale cookie behind.
func (s *session) Destroy(ctx context.Context) error {
	s.mod.clearCookie(s.w)
	if s.id == "" {
		return nil
	}
	return s.mod.store.Delete(ctx, s.id)
}
