package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/webisnoteasy/PgBase/internal/usermigrate"
	"github.com/webisnoteasy/PgBase/pgbase"
)

const userMigrationUsage = "usage: pgbase migration create NAME [DIRECTORY] | pgbase migration list | pgbase migration up | pgbase migration down"

func runUserMigration(ctx context.Context, args []string, env environment, streams IO) error {
	if len(args) >= 1 && args[0] == "create" {
		if len(args) < 2 || len(args) > 3 {
			return configurationError{err: errors.New("invalid user migration create usage"), message: userMigrationUsage}
		}
		directory := "migrations"
		if len(args) == 3 {
			directory = filepath.Clean(args[2])
		}
		path, err := usermigrate.Generate(directory, args[1], time.Now())
		if err != nil {
			return configurationError{err: err, message: userMigrationUsage}
		}
		_, err = fmt.Fprintln(streams.stdout(), path)
		return err
	}
	if len(args) != 1 || args[0] != "list" && args[0] != "up" && args[0] != "down" {
		return configurationError{err: errors.New("invalid user migration usage"), message: userMigrationUsage}
	}
	runtime, err := openDatabase(ctx, env, streams)
	if err != nil {
		return err
	}
	options := runtime.options
	runtime.Close()
	app, err := pgbase.New(ctx, options)
	if err != nil {
		return operationError(options.Logger, "open PgBase application for user migrations", err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	manifest := pgbase.RegisteredUserMigrations()
	if len(manifest) == 0 {
		return configurationError{err: errors.New("no user migrations are compiled into this executable"), message: "no user migrations are compiled into this executable"}
	}
	runner, err := pgbase.NewUserMigrationRunner(app, manifest)
	if err != nil {
		return operationError(options.Logger, "validate user migration manifest", err)
	}
	switch args[0] {
	case "up":
		results, err := runner.Up(ctx)
		if err != nil {
			return operationError(options.Logger, "apply user migrations", err)
		}
		for _, result := range results {
			if _, err := fmt.Fprintf(streams.stdout(), "up %s %s\n", result.Migration.ID, hex.EncodeToString(result.Migration.Revision[:])); err != nil {
				return err
			}
		}
	case "down":
		result, err := runner.Down(ctx)
		if err != nil {
			return operationError(options.Logger, "revert user migration", err)
		}
		_, err = fmt.Fprintf(streams.stdout(), "down %s %s\n", result.Migration.ID, hex.EncodeToString(result.Migration.Revision[:]))
		return err
	case "list":
		entries, err := runner.List(ctx)
		if err != nil {
			return operationError(options.Logger, "list user migrations", err)
		}
		for _, entry := range entries {
			state := "pending"
			if entry.Applied {
				state = "applied"
			}
			if _, err := fmt.Fprintf(streams.stdout(), "%s %s %s\n", state, entry.ID, hex.EncodeToString(entry.Revision[:])); err != nil {
				return err
			}
		}
	}
	return nil
}
