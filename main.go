package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/leezenn/slk/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cmd.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
