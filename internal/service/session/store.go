package session

import (
	"context"
	"time"
)

// Session is a per-request session view. Values live in the backing Store; the
// cookie carries only the signed session ID. Call Save to persist mutations.
type Session interface {
	// ID is empty before the session is first saved.
	ID() string
	Get(key string) (any, bool)
	Set(key string, value any)
	Delete(key string)
	// Save persists the session; if new, the Store assigns and returns the ID.
	Save(ctx context.Context) (string, error)
	// Destroy deletes the session from the Store (the caller clears the cookie).
	Destroy(ctx context.Context) error
}

// Store is the pluggable session backing. memory (dev) and db (prod) ship with
// the template; other backends (Redis…) can satisfy it without touching Service.
type Store interface {
	// Load reports exists=false for expired or unknown sessions.
	Load(ctx context.Context, id string) (values map[string]any, expiresAt time.Time, exists bool, err error)
	// Save allocates a new id when id is empty; otherwise updates and refreshes expiry to now+ttl.
	Save(ctx context.Context, id string, values map[string]any, ttl time.Duration) (string, error)
	Delete(ctx context.Context, id string) error
}
