package app

import (
	"log/slog"
	"testing"
)

// TestNewServices_StartsEmpty pins the container's contract: Log is the only
// field known at construction time, and every optional field starts nil.
//
// This is what lets an optional component be removed without touching its
// consumers — a stripped component leaves a nil field instead of a dangling
// reference to a deleted package. Nothing here may construct a component.
func TestNewServices_StartsEmpty(t *testing.T) {
	log := slog.Default()

	svc := NewServices(log)

	if svc.Log != log {
		t.Error("Log not wired")
	}
	if svc.DB != nil {
		t.Error("DB should be nil until the db component assigns it after Start")
	}
	//goappctl:session
	if svc.Session != nil {
		t.Error("Session should be nil until the session component assigns it")
	}
	//goappctl:end
	//goappctl:queue
	if svc.Queue != nil {
		t.Error("Queue should be nil until the queue component assigns it")
	}
	//goappctl:end
}
