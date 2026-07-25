package session

import (
	"context"
	"net/http"
)

// session is the per-request Session implementation. The middleware injects the
// response writer (w) so Save/Destroy emit Set-Cookie synchronously — inertia's
// writer is write-through, so a cookie set after the handler writes its body
// would be dropped.
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

func (s *session) Flash(kind, message string) {
	s.Set(flashPrefix+kind, message)
}

// Save persists the session and writes the signed cookie. Call before the
// handler writes its body so Set-Cookie lands before the header flushes.
func (s *session) Save(ctx context.Context) (string, error) {
	id, err := s.mod.store.Save(ctx, s.id, s.values, s.mod.ttl())
	if err != nil {
		return "", err
	}
	s.id = id
	s.mod.setCookie(s.w, id)
	return id, nil
}

// Destroy removes the session from the store and clears the response cookie
// (cleared even if the session was never persisted, so no stale cookie remains).
func (s *session) Destroy(ctx context.Context) error {
	s.mod.clearCookie(s.w)
	if s.id == "" {
		return nil
	}
	return s.mod.store.Delete(ctx, s.id)
}
