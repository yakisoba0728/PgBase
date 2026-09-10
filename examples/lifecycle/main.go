// Example lifecycle extensions. Configure the usual PGBASE_* environment and
// run `go run ./examples/lifecycle`; add PGBASE_BACKUPS_DIRECTORY for web restore.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yakisoba0728/PgBase/internal/config"
	"github.com/yakisoba0728/PgBase/pgbase"
)

func main() {
	options, err := config.LoadEnv(os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}
	hooks := &pgbase.HookRegistry{}
	options.Hooks = hooks
	_, err = hooks.On(pgbase.HookRecordAfter, pgbase.Hook{After: func(_ context.Context, event *pgbase.HookEvent) {
		if event.Err == nil {
			mutation := event.Data.(pgbase.Mutation)
			log.Printf("committed %s in %s (event %s)", mutation.Operation, mutation.Collection, mutation.EventID)
		}
	}})
	if err != nil {
		log.Fatal(err)
	}
	_, err = hooks.On(pgbase.HookMailSend, pgbase.Hook{Before: func(_ context.Context, event *pgbase.HookEvent) error {
		message := event.Data.(*pgbase.MailMessage)
		message.Subject = "[Example] " + message.Subject
		return nil
	}})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = pgbase.RunManaged(ctx, options); err != nil {
		log.Fatal(err)
	}
}
