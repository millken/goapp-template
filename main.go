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

	// Own SIGINT + SIGTERM here; the context threads through cobra so a slow
	// startup is interruptible. eng.Serve owns HTTP-level signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		cancel()
		os.Exit(1)
	}
}
