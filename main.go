package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/millken/goapp-template/commands"
)

func main() {
	root := commands.New()

	// Single signal owner at this level: SIGINT + SIGTERM. The context is
	// threaded through cobra into app.Serve, where it is consumed by Boot (so a
	// slow startup is interruptible). eng.Serve owns HTTP-level signal handling
	// internally; app.Serve builds a fresh timeout context for Shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		cancel()
		os.Exit(1)
	}
}
