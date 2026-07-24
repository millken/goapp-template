package session

import (
	"context"
	"time"
)

// Session is a per-request session view. Values are stored in the backing Store
// (memory or db); the cookie only carries the signed session ID.
//
// Save persists any mutations to the Store. Handlers should call Save when they
// modified the session (e.g. on login); unchanged sessions need not be saved.
type Session interface {
	// ID is the session's identifier (stable across requests for the same
	// client cookie). Empty before the session is first saved.
	ID() string

	// Get returns the value for key and whether it existed.
	Get(key string) (any, bool)

	// Set stores a value for key.
	Set(key string, value any)

	// Delete removes a key. It is a no-op if the key is absent.
	Delete(key string)

	// Save persists the session to the Store. If the session is new and has no
	// ID, the Store assigns one; the returned ID is what should be written to
	// the response cookie.
	Save(ctx context.Context) (string, error)

	// Destroy deletes the session from the Store. The caller should also clear
	// the response cookie.
	Destroy(ctx context.Context) error
}

// Store is the pluggable session backing. Two implementations ship with the
// template: memory (development default) and db (production, via db.Provider).
// Additional backends (Redis…) can satisfy this interface without touching the
// Module.
type Store interface {
	// Load fetches the session data for id, returning the values and whether
	// the session existed (expired or unknown sessions report exists=false).
	Load(ctx context.Context, id string) (values map[string]any, expiresAt time.Time, exists bool, err error)

	// Save persists the session. If id is empty the Store allocates a new one
	// and returns it; otherwise it updates the existing session and refreshes
	// the expiry to now+ttl. The returned id is the canonical session id.
	Save(ctx context.Context, id string, values map[string]any, ttl time.Duration) (string, error)

	// Delete removes the session for id. It is a no-op if the session is absent.
	Delete(ctx context.Context, id string) error
}
