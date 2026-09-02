package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/webisnoteasy/PgBase/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.LookupEnv, cli.SystemIO())
	stop()
	os.Exit(code)
}
