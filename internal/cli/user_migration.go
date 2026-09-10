package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yakisoba0728/PgBase/internal/config"
	"github.com/yakisoba0728/PgBase/internal/usermigrate"
	"github.com/yakisoba0728/PgBase/pgbase"
)

const userMigrationUsage = "usage: pgbase migration create NAME [DIRECTORY] | pgbase migration snapshot FILE | pgbase migration diff NAME BEFORE AFTER [DIRECTORY] | pgbase migration <history|history-sync ID|auto-status|recover|list|up|down> [--js-dir DIR]"

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
	if len(args) >= 1 && args[0] == "diff" {
		if len(args) < 4 || len(args) > 5 {
			return configurationError{err: errors.New("invalid user migration diff usage"), message: userMigrationUsage}
		}
		before, err := os.ReadFile(args[2])
		if err != nil {
			return configurationError{err: err, message: "read before migration snapshot"}
		}
		after, err := os.ReadFile(args[3])
		if err != nil {
			return configurationError{err: err, message: "read after migration snapshot"}
		}
		directory := "migrations"
		if len(args) == 5 {
			directory = filepath.Clean(args[4])
		}
		generated, path, err := usermigrate.GenerateFromSnapshots(directory, "migrations", args[1], time.Now(), before, after)
		if err != nil {
			return configurationError{err: err, message: "generate migration from snapshots"}
		}
		if _, err := fmt.Fprintln(streams.stdout(), path); err != nil {
			return err
		}
		for _, warning := range generated.Impact.DataLoss {
			if _, err := fmt.Fprintln(streams.stderr(), "data-loss:", warning); err != nil {
				return err
			}
		}
		for _, reason := range generated.Impact.Irreversible {
			if _, err := fmt.Fprintln(streams.stderr(), "irreversible-down:", reason); err != nil {
				return err
			}
		}
		return nil
	}
	if len(args) == 0 {
		return configurationError{err: errors.New("invalid user migration usage"), message: userMigrationUsage}
	}
	var javaScriptDirectory string
	args, javaScriptDirectory, err := parseUserMigrationJavaScriptDirectory(args)
	if err != nil {
		return configurationError{err: err, message: userMigrationUsage}
	}
	if args[0] == "snapshot" && len(args) != 2 || args[0] == "history-sync" && len(args) != 2 ||
		args[0] != "snapshot" && args[0] != "history-sync" && (len(args) != 1 || args[0] != "list" && args[0] != "up" && args[0] != "down" && args[0] != "history" && args[0] != "auto-status" && args[0] != "recover") {
		return configurationError{err: errors.New("invalid user migration usage"), message: userMigrationUsage}
	}
	javaScriptOptions, err := preflightUserMigrationJavaScript(ctx, env, javaScriptDirectory)
	if err != nil {
		return configurationError{err: err, message: "JavaScript bundle is invalid: " + err.Error()}
	}
	runtime, err := openDatabase(ctx, env, streams)
	if err != nil {
		return err
	}
	options := runtime.options
	runtime.Close()
	if javaScriptOptions != nil {
		options.JavaScript = javaScriptOptions
	}
	app, err := pgbase.New(ctx, options)
	if err != nil {
		return operationError(options.Logger, "open PgBase application for user migrations", err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	if args[0] == "snapshot" {
		snapshot, err := app.UserMigrationSnapshot(ctx)
		if err != nil {
			return operationError(options.Logger, "snapshot collection schema", err)
		}
		if args[1] == "-" {
			_, err = streams.stdout().Write(append(snapshot, '\n'))
			return err
		}
		file, err := os.OpenFile(filepath.Clean(args[1]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return configurationError{err: err, message: "create migration snapshot"}
		}
		if _, err = file.Write(append(snapshot, '\n')); err == nil {
			err = file.Close()
		} else {
			_ = file.Close()
		}
		if err != nil {
			return operationError(options.Logger, "write migration snapshot", err)
		}
		_, err = fmt.Fprintln(streams.stdout(), filepath.Clean(args[1]))
		return err
	}
	if args[0] == "auto-status" {
		statuses, err := app.UserMigrationSourceStatus(ctx)
		if err != nil {
			return operationError(options.Logger, "read automatic user migration source status", err)
		}
		for _, status := range statuses {
			if _, err := fmt.Fprintf(streams.stdout(), "%s %s attempts=%d file=%s\n", status.Status, status.MigrationID, status.Attempts, status.Filename); err != nil {
				return err
			}
		}
		return nil
	}
	if args[0] == "recover" {
		if err := app.RecoverUserMigrationSources(ctx); err != nil {
			return operationError(options.Logger, "recover automatic user migration sources", err)
		}
		_, err := fmt.Fprintln(streams.stdout(), "automatic user migration source recovery complete")
		return err
	}
	manifest := pgbase.RegisteredUserMigrations()
	javaScriptMigrations, err := app.JavaScriptMigrations()
	if err != nil {
		return operationError(options.Logger, "load JavaScript user migrations", err)
	}
	manifest = append(manifest, javaScriptMigrations...)
	if len(manifest) == 0 && args[0] != "history" {
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
	case "history":
		entries, err := runner.History(ctx)
		if err != nil {
			return operationError(options.Logger, "compare user migration source and history", err)
		}
		for _, entry := range entries {
			source, applied := "-", "-"
			if entry.SourceRevision != nil {
				source = hex.EncodeToString(entry.SourceRevision[:])
			}
			if len(entry.AppliedRevision) != 0 {
				applied = hex.EncodeToString(entry.AppliedRevision)
			}
			if _, err := fmt.Fprintf(streams.stdout(), "%s %s source=%s history=%s\n", entry.State, entry.ID, source, applied); err != nil {
				return err
			}
		}
	case "history-sync":
		entry, err := runner.HistorySync(ctx, args[1])
		if err != nil {
			return operationError(options.Logger, "synchronize user migration history", err)
		}
		_, err = fmt.Fprintf(streams.stdout(), "synced %s %s\n", entry.ID, hex.EncodeToString(entry.Revision[:]))
		return err
	}
	return nil
}

func preflightUserMigrationJavaScript(ctx context.Context, env environment, directory string) (*pgbase.JavaScriptOptions, error) {
	lookup := env
	if directory != "" {
		lookup = func(name string) (string, bool) {
			if name == "PGBASE_JS_DIR" {
				return directory, true
			}
			if env == nil {
				return "", false
			}
			return env(name)
		}
	}
	options, err := config.LoadJavaScriptEnv(lookup)
	if err != nil || options == nil {
		return options, err
	}
	if _, err := pgbase.ValidateJavaScript(ctx, *options); err != nil {
		return nil, err
	}
	return options, nil
}

func parseUserMigrationJavaScriptDirectory(args []string) ([]string, string, error) {
	if len(args) <= 1 {
		return args, "", nil
	}
	result := []string{args[0]}
	directory := ""
	for index := 1; index < len(args); index++ {
		if args[index] != "--js-dir" {
			result = append(result, args[index])
			continue
		}
		if directory != "" || index+1 >= len(args) || args[index+1] == "" {
			return nil, "", errors.New("invalid --js-dir")
		}
		directory = args[index+1]
		index++
	}
	return result, directory, nil
}
