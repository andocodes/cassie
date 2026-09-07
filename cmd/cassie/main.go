package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/andocodes/cassie/internal/ui/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root, err := cli.New(version)
	if err == nil {
		err = root.ExecuteContext(ctx)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "cassie:", err)
		os.Exit(cli.ExitCode(err))
	}
}
