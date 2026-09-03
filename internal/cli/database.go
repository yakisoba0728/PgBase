package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/webisnoteasy/PgBase/internal/config"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/pgbase"
)

// databaseRuntime is the configuration, redacting logger and connection pool a
// database command needs. Every such command bootstraps through openDatabase so
// the order of the steps, and the exit code each failure produces, is decided
// once instead of per command.
type databaseRuntime struct {
	options pgbase.Options
	logger  *slog.Logger
	pool    *database.Pool
}

// Close releases the pool. The runtime is unusable afterwards.
func (runtime *databaseRuntime) Close() {
	if runtime != nil && runtime.pool != nil {
		runtime.pool.Close()
	}
}

// bootstrapError says whether the invocation was rejected before it ever
// reached PostgreSQL. Commands that print a machine-readable report use it to
// choose their finding; everyone else just returns it, and the exit code comes
// from the configurationError it wraps.
type bootstrapError struct {
	configuration bool
	err           error
}

func (e bootstrapError) Error() string { return e.err.Error() }

func (e bootstrapError) Unwrap() error { return e.err }

// openDatabase loads the environment, installs the redacting JSON logger on
// stderr, validates the pool configuration and opens the pool. A configuration
// rejection exits 2 and an unreachable database exits 1, so the two failures
// are reported separately even though both leave the caller without a pool.
func openDatabase(ctx context.Context, env environment, streams IO) (*databaseRuntime, error) {
	options, err := config.LoadEnv(env)
	if err != nil {
		return nil, bootstrapError{configuration: true, err: configurationError{err: err}}
	}
	options.Logger = observability.NewJSONLogger(streams.stderr())
	pool, err := database.Open(ctx, options.DatabaseURL, options.StatementTimeout, options.AllowInsecureDatabase)
	if err != nil {
		configuration := database.Classify(err) == database.KindValidation
		var reported error = operationError(options.Logger, "open PostgreSQL pool", err)
		if configuration {
			reported = configurationError{err: reported}
		}
		return nil, bootstrapError{
			configuration: configuration,
			err:           reported,
		}
	}
	return &databaseRuntime{options: options, logger: options.Logger, pool: pool}, nil
}

func rejectedBeforeConnecting(err error) bool {
	var bootstrap bootstrapError
	return errors.As(err, &bootstrap) && bootstrap.configuration
}

// operationError logs an operator diagnostic through the redacting JSON logger
// and returns the wrapped cause. Every command failure that has a configured
// logger reports through it, including the pool-configuration rejections that
// exit as configuration errors, so no command exits silently.
func operationError(logger *slog.Logger, message string, err error) error {
	logger.Error(message, "error", err)
	return fmt.Errorf("%s: %w", message, err)
}
