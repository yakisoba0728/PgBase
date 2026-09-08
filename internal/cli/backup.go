package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/webisnoteasy/PgBase/internal/backup"
	"github.com/webisnoteasy/PgBase/internal/config"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

func runBackup(ctx context.Context, args []string, env environment, streams IO) (resultErr error) {
	if len(args) == 0 || (args[0] != "create" && args[0] != "restore" && args[0] != "verify") {
		return backupUsage()
	}
	operation := args[0]
	flags := flag.NewFlagSet("backup "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *directory == "" {
		return backupUsage()
	}
	if operation == "verify" {
		manifest, err := backup.Verify(*directory)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(streams.stdout(), "backup verified: PostgreSQL %d, %d files, created %s\n", manifest.PostgreSQLMajor, len(manifest.Files), manifest.CreatedAt.Format("2006-01-02T15:04:05Z"))
		return err
	}
	options, err := config.LoadEnv(env)
	if err != nil {
		return configurationError{err: err}
	}
	options.Logger = observability.NewJSONLogger(streams.stderr())
	files := ""
	if options.Files.Driver != "" {
		if options.Files.Driver != "local" {
			return configurationError{err: errors.New("backup supports only local file storage"), message: "backup supports only local file storage"}
		}
		files = options.Files.LocalDirectory
	}
	gate, err := database.TryBackupGate(ctx, options.DatabaseURL, true)
	if errors.Is(err, database.ErrBackupGateBusy) {
		return operationError(options.Logger, "acquire exclusive backup gate", err)
	}
	if err != nil {
		return operationError(options.Logger, "connect to PostgreSQL", err)
	}
	defer func() { resultErr = errors.Join(resultErr, gate.Close(context.Background())) }()
	err = runMonitoredBackup(ctx, gate, func(operationContext context.Context) error {
		if operation == "create" {
			return backup.Create(operationContext, *directory, options.DatabaseURL, files)
		}
		return backup.Restore(operationContext, *directory, options.DatabaseURL, files)
	})
	if err != nil {
		return operationError(options.Logger, operation+" backup", err)
	}
	_, err = fmt.Fprintf(streams.stdout(), "backup %s complete: %s\n", operation, *directory)
	return err
}

type gateMonitor interface{ Run(context.Context) error }

func runMonitoredBackup(ctx context.Context, gate gateMonitor, operation func(context.Context) error) error {
	operationContext, cancelOperation := context.WithCancelCause(ctx)
	gateResult := make(chan error, 1)
	go func() {
		err := gate.Run(operationContext)
		gateResult <- err
		if err != nil && !errors.Is(err, context.Canceled) {
			cancelOperation(err)
		}
	}()
	err := operation(operationContext)
	cancelOperation(context.Canceled)
	gateErr := <-gateResult
	if cause := context.Cause(operationContext); cause != nil && !errors.Is(cause, context.Canceled) {
		err = errors.Join(err, cause)
	} else if gateErr != nil && !errors.Is(gateErr, context.Canceled) {
		err = errors.Join(err, gateErr)
	}
	return err
}

func backupUsage() error {
	return configurationError{err: errors.New("invalid backup usage"), message: "usage: pgbase backup <create|restore|verify> --directory DIRECTORY"}
}
