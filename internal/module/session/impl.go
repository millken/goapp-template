package session

import (
	"context"
)

// session is the concrete Session implementation. It is created per request by
// the middleware, holds a snapshot of the store data, and writes back on Save.
type session struct {
	id     string
	values map[string]any
	mod    *Module // for store access and cookie writing
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

// Save persists the session to the store. If the session is new (no id) the
// store assigns one; the signed cookie is written to the response via the
// module. Call from a handler that has the inertia.Context (the middleware
// stores the session there).
func (s *session) Save(ctx context.Context) (string, error) {
	id, err := s.mod.store.Save(ctx, s.id, s.values, s.mod.ttl())
	if err != nil {
		return "", err
	}
	s.id = id
	return id, nil
}

// Destroy removes the session from the store. The caller should also clear the
// response cookie (the module exposes clearCookie for this via the Provider).
func (s *session) Destroy(ctx context.Context) error {
	if s.id == "" {
		return nil
	}
	return s.mod.store.Delete(ctx, s.id)
}
