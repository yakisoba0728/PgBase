// Command jobs starts pgBase with one durable local cleanup job.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yakisoba0728/PgBase/internal/config"
	"github.com/yakisoba0728/PgBase/pgbase"
)

func main() {
	options, err := config.LoadEnv(os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}
	options.JobsEnabled = true
	options.Jobs = []pgbase.ScheduledJob{{
		Name:     "example-cleanup",
		Schedule: pgbase.JobSchedule(time.Minute),
		Handler: func(ctx context.Context, run pgbase.JobRun) error {
			// Store run.RunKey() with an external effect before performing it.
			// That makes a retry after a crash harmless.
			log.Printf("cleanup job %s fence %d", run.Name, run.FenceGeneration)
			return ctx.Err()
		},
	}}
	app, err := pgbase.New(context.Background(), options)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
