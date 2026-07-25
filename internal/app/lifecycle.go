package app

import "context"

// Lifecycle is the infrastructure contract: Start acquires process-lifetime
// resources (DB pool, migrations, session store); Stop releases them. Only
// infrastructure services (db, session) implement it — features are plain
// handler methods. serve.go drives Start/Stop explicitly in dependency order.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
