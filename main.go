package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Kaylebor/wirecmd/internal/cli"
)

// Command wirecmd exposes configured capabilities through a shell-native
// interface.
func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Run(ctx, args, os.Stdin, os.Stdout, os.Stderr)
}
