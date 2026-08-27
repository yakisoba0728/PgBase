// Package cli provides command dispatch for the pgbase executable.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/webisnoteasy/PgBase/internal/config"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/pgbase"
)

// Command is a pgbase CLI command handler.
type Command func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error

// CommandSet contains the handlers exposed by the pgbase command line.
type CommandSet struct {
	Serve       Command
	Migrate     Command
	Admin       Command
	Doctor      Command
	Healthcheck Command
}

// DefaultCommands returns the command handlers wired into the production binary.
// Commands are added by their owning packages in later implementation tasks.
func DefaultCommands() CommandSet {
	return CommandSet{Serve: runServe, Migrate: runMigrate}
}

// Run dispatches a command and returns its process exit code.
func Run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer, commands CommandSet) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	name := args[0]
	var command Command
	switch name {
	case "serve":
		command = commands.Serve
	case "migrate":
		command = commands.Migrate
	case "admin":
		command = commands.Admin
	case "doctor":
		command = commands.Doctor
	case "healthcheck":
		command = commands.Healthcheck
	default:
		fmt.Fprintf(stderr, "pgbase: unknown command %q\n", name)
		printUsage(stderr)
		return 2
	}

	if command == nil {
		fmt.Fprintf(stderr, "pgbase: command %q is unavailable\n", name)
		return 1
	}
	if err := command(ctx, args[1:], lookup, stdout, stderr); err != nil {
		var configErr configurationError
		if errors.As(err, &configErr) {
			message := configErr.message
			if message == "" {
				message = "invalid configuration"
			}
			fmt.Fprintf(stderr, "pgbase: %s\n", message)
			return 2
		}
		fmt.Fprintln(stderr, "pgbase: command failed")
		return 1
	}
	return 0
}

type configurationError struct {
	err     error
	message string
}

func (e configurationError) Error() string { return e.err.Error() }

func (e configurationError) Unwrap() error { return e.err }

func runServe(ctx context.Context, args []string, lookup func(string) (string, bool), _ io.Writer, stderr io.Writer) error {
	if len(args) != 0 {
		return configurationError{err: errors.New("invalid serve usage"), message: "usage: pgbase serve"}
	}
	options, err := config.LoadEnv(lookup)
	if err != nil {
		return configurationError{err: err}
	}
	options.Logger = observability.NewJSONLogger(stderr)
	app, err := pgbase.New(ctx, options)
	if err != nil {
		options.Logger.Error("construct pgbase application", "error", err)
		return fmt.Errorf("construct pgbase application: %w", err)
	}
	if err := app.Run(ctx); err != nil {
		options.Logger.Error("run pgbase application", "error", err)
		return fmt.Errorf("run pgbase application: %w", err)
	}
	return nil
}

func runMigrate(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, _ io.Writer) error {
	if len(args) != 1 || (args[0] != "status" && args[0] != "up") {
		return configurationError{err: errors.New("invalid migrate usage"), message: "usage: pgbase migrate <status|up>"}
	}

	options, err := config.LoadEnv(lookup)
	if err != nil {
		return configurationError{err: err}
	}
	if _, err := pgxpool.ParseConfig(options.DatabaseURL); err != nil {
		return configurationError{err: fmt.Errorf("parse PostgreSQL pool configuration: %w", err)}
	}
	pool, err := database.Open(ctx, options.DatabaseURL, options.StatementTimeout)
	if err != nil {
		return migrationOperationError(options.Logger, "open PostgreSQL pool", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return migrationOperationError(options.Logger, "ping PostgreSQL pool", err)
	}

	runner := migrate.New(pool, options.Logger)
	if args[0] == "up" {
		if err := runner.Up(ctx); err != nil {
			return migrationOperationError(options.Logger, "apply system migrations", err)
		}
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return migrationOperationError(options.Logger, "read system migration status", err)
	}
	_, err = fmt.Fprintf(stdout, "system migrations: current=%d latest=%d dirty=%t\n", status.Current, status.Latest, status.Dirty)
	if err != nil {
		return migrationOperationError(options.Logger, "write migration status", err)
	}
	return nil
}

func migrationOperationError(logger *slog.Logger, message string, err error) error {
	logger.Error(message, "error", err)
	return fmt.Errorf("%s: %w", message, err)
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: pgbase <serve, migrate, admin, doctor> [arguments]")
}
