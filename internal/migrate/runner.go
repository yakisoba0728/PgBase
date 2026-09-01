// Package migrate applies and inspects pgBase's embedded system migrations.
package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

const advisoryLockKey int64 = 0x7067626173650001

const bootstrapSQL = `
CREATE SCHEMA IF NOT EXISTS pgbase_meta;
CREATE TABLE IF NOT EXISTS pgbase_meta.system_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum bytea NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    execution_ms bigint NOT NULL CHECK (execution_ms >= 0)
);`

// Runner applies the system schema migrations using the shared database pool.
type Runner struct {
	pool          *database.Pool
	logger        *slog.Logger
	migrations    []system.Migration
	compatibility *historyCompatibilityPolicy
	recorder      observability.Recorder
}

// New creates a system migration runner.
func New(pool *database.Pool, logger *slog.Logger) *Runner {
	return NewWithRecorder(pool, logger, nil)
}

// NewWithRecorder creates a system migration runner with bounded telemetry.
func NewWithRecorder(pool *database.Pool, logger *slog.Logger, recorder observability.Recorder) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		pool:          pool,
		logger:        logger,
		migrations:    system.Migrations(),
		compatibility: recordMembershipPreparationPolicy(system.Migrations()),
		recorder:      recorder,
	}
}

// Up applies every pending embedded migration atomically.
func (r *Runner) Up(ctx context.Context) (err error) {
	started := time.Now()
	defer r.finishOperation(ctx, observability.OperationMigrationUp, started, &err)
	if r == nil || r.pool == nil {
		return fmt.Errorf("migration pool is nil")
	}
	if err := validateMigrationManifest(r.migrations); err != nil {
		return err
	}

	type appliedMigration struct {
		version     int64
		name        string
		executionMS int64
	}
	appliedMigrations := make([]appliedMigration, 0)
	err = r.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
			return fmt.Errorf("set migration lock timeout: %w", err)
		}
		if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
			return fmt.Errorf("disable request statement timeout for migrations: %w", err)
		}
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", advisoryLockKey); err != nil {
			return fmt.Errorf("acquire migration advisory lock: %w", err)
		}
		if _, err := tx.Exec(ctx, bootstrapSQL); err != nil {
			return fmt.Errorf("bootstrap system migration history: %w", err)
		}

		applied, err := readApplied(ctx, tx)
		if err != nil {
			return err
		}
		history := inspectHistory(r.migrations, applied)
		if history.err != nil {
			return history.err
		}
		if history.issue != nil {
			return history.issue
		}
		for _, migration := range r.migrations {
			if _, ok := history.applied[migration.Version]; ok {
				continue
			}

			started := time.Now()
			if _, err := tx.Exec(ctx, migration.SQL); err != nil {
				return fmt.Errorf("apply system migration %d (%s): %w", migration.Version, migration.Name, err)
			}
			executionMS := time.Since(started).Milliseconds()
			if _, err := tx.Exec(ctx, `
				insert into pgbase_meta.system_migrations (version, name, checksum, execution_ms)
				values ($1, $2, $3, $4)`, migration.Version, migration.Name, migration.SHA256[:], executionMS); err != nil {
				return fmt.Errorf("record system migration %d (%s): %w", migration.Version, migration.Name, err)
			}
			appliedMigrations = append(appliedMigrations, appliedMigration{version: migration.Version, name: migration.Name, executionMS: executionMS})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, migration := range appliedMigrations {
		r.logger.Info("applied system migration", "version", migration.version, "name", migration.name, "execution_ms", migration.executionMS)
	}
	return nil
}

func (r *Runner) record(ctx context.Context, event observability.Event) {
	if r != nil && r.recorder != nil {
		r.recorder.Record(ctx, event)
	}
}

func (r *Runner) finishOperation(ctx context.Context, operation observability.Operation, started time.Time, operationErr *error) {
	if recovered := recover(); recovered != nil {
		r.record(ctx, observability.OperationEvent(observability.ComponentMigration, operation, observability.OutcomeFailure, time.Since(started)))
		panic(recovered)
	}
	var err error
	if operationErr != nil {
		err = *operationErr
	}
	r.record(ctx, observability.OperationEvent(observability.ComponentMigration, operation, migrationOperationOutcome(err), time.Since(started)))
}

func migrationOperationOutcome(err error) observability.Outcome {
	if err == nil {
		return observability.OutcomeSuccess
	}
	switch database.Classify(err) {
	case database.KindValidation:
		return observability.OutcomeValidation
	case database.KindConflict:
		return observability.OutcomeConflict
	case database.KindUnavailable:
		return observability.OutcomeUnavailable
	case database.KindCanceled:
		return observability.OutcomeCanceled
	default:
		return observability.OutcomeFailure
	}
}

func readApplied(ctx context.Context, db database.DBTX) (map[int64][]byte, error) {
	rows, err := db.Query(ctx, "select version, checksum from pgbase_meta.system_migrations")
	if err != nil {
		return nil, fmt.Errorf("read applied system migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64][]byte)
	var duplicateVersion, latestVersion int64
	var duplicateFound bool
	for rows.Next() {
		var version int64
		var checksum []byte
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan applied system migration: %w", err)
		}
		if version > latestVersion {
			latestVersion = version
		}
		if _, exists := applied[version]; exists {
			if !duplicateFound || version < duplicateVersion {
				duplicateVersion = version
			}
			duplicateFound = true
			continue
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied system migrations: %w", err)
	}
	if duplicateFound {
		return nil, &HistoryIssue{
			Kind:           HistoryIssueDuplicateVersion,
			Version:        duplicateVersion,
			appliedVersion: latestVersion,
		}
	}
	return applied, nil
}

func readAppliedIfPresent(ctx context.Context, db database.DBTX) (map[int64][]byte, bool, error) {
	var historyTable *string
	if err := db.QueryRow(ctx, "select to_regclass('pgbase_meta.system_migrations')::text").Scan(&historyTable); err != nil {
		return nil, false, fmt.Errorf("check system migration history: %w", err)
	}
	if historyTable == nil {
		return nil, false, nil
	}
	applied, err := readApplied(ctx, db)
	if err != nil {
		return nil, true, err
	}
	return applied, true, nil
}
