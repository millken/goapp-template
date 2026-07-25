package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"sync"
	"time"
)

// MemoryStore is an in-process session store: the development default, but
// loses sessions on restart and does not share state across instances.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]memorySession
}

type memorySession struct {
	values    map[string]any
	expiresAt time.Time
}

// NewMemoryStore returns an empty in-memory session store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]memorySession)}
}

func (s *MemoryStore) Load(_ context.Context, id string) (map[string]any, time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, time.Time{}, false, nil
	}
	if !sess.expiresAt.IsZero() && time.Now().After(sess.expiresAt) {
		delete(s.sessions, id)
		return nil, time.Time{}, false, nil
	}
	// Return a copy so callers mutate without holding the lock until Save.
	out := make(map[string]any, len(sess.values))
	maps.Copy(out, sess.values)
	return out, sess.expiresAt, true, nil
}

func (s *MemoryStore) Save(_ context.Context, id string, values map[string]any, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		newID, err := randomID()
		if err != nil {
			return "", fmt.Errorf("session: generate id: %w", err)
		}
		id = newID
	}
	// Copy so the caller's map is not retained by reference.
	stored := make(map[string]any, len(values))
	maps.Copy(stored, values)
	s.sessions[id] = memorySession{
		values:    stored,
		expiresAt: time.Now().Add(ttl),
	}
	return id, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

// randomID returns a fresh 32-byte hex session ID. It errors rather than return
// a predictable fallback if the CSPRNG fails — a guessable ID is worse than none.
func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
