package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/millken/goapp-template/commands"
)

func main() {
	root := commands.New()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		cancel()
		os.Exit(1)
	}
}
