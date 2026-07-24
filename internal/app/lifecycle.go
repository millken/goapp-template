package app

import "context"

// Lifecycle is the only infrastructure contract in the codegen architecture.
// Start acquires process-lifetime resources (open a DB pool, run migrations,
// resolve a session store); Stop releases them. It replaces the old
// Module/Booter/Shutdowner trio and the Boot/Shutdown method names.
//
// Features (controllers) do NOT implement Lifecycle — they are just methods.
// Only the handful of infrastructure services (db, session) implement it, and
// serve.go drives them explicitly in dependency order (Stop runs in reverse via
// defer). See docs/design/opencart-codegen.md §4.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}
