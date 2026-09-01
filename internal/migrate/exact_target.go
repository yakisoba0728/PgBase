package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/observability"
)

// HistoryState is the checksum-pinned migration history at one point in time.
type HistoryState struct {
	Version  int64
	Checksum [32]byte
	Dirty    bool
}

// UpThroughReceipt records the committed history observed before and after an
// exact-target migration attempt.
type UpThroughReceipt struct {
	Target int64
	Before HistoryState
	After  HistoryState
}

// UpThrough applies the latest embedded migration only when the committed
// history is clean and exactly one manifest entry behind the target.
func (r *Runner) UpThrough(ctx context.Context, target int64) (receipt UpThroughReceipt, err error) {
	started := time.Now()
	defer r.finishOperation(ctx, observability.OperationMigrationUp, started, &err)
	receipt.Target = target
	if r == nil || r.pool == nil {
		return receipt, fmt.Errorf("migration pool is nil")
	}
	manifest := inspectHistory(r.migrations, nil)
	if manifest.err != nil {
		return receipt, manifest.err
	}
	if len(r.migrations) == 0 || manifest.latest != target {
		return receipt, fmt.Errorf("system migration target %d is not the latest embedded version", target)
	}
	targetMigration := r.migrations[len(r.migrations)-1]
	previousVersion := int64(0)
	if len(r.migrations) > 1 {
		previousVersion = r.migrations[len(r.migrations)-2].Version
	}
	applied, _, err := readAppliedIfPresent(ctx, r.pool)
	if err != nil {
		return receipt, err
	}
	preflight := inspectHistory(r.migrations, applied)
	if preflight.err != nil {
		return receipt, preflight.err
	}
	receipt.Before = preflight.state
	if err := exactTargetPrecondition(preflight, target, previousVersion); err != nil {
		return receipt, err
	}

	appliedTarget := false
	var executionMS int64
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
		applied, present, err := readAppliedIfPresent(ctx, tx)
		if err != nil {
			return err
		}
		if !present {
			if _, err := tx.Exec(ctx, bootstrapSQL); err != nil {
				return fmt.Errorf("bootstrap system migration history: %w", err)
			}
			applied, err = readApplied(ctx, tx)
			if err != nil {
				return err
			}
		}
		inspection := inspectHistory(r.migrations, applied)
		receipt.Before = inspection.state
		if inspection.err != nil {
			return inspection.err
		}
		if err := exactTargetPrecondition(inspection, target, previousVersion); err != nil {
			return err
		}
		if inspection.state.Version == target {
			return nil
		}

		migrationStarted := time.Now()
		if _, err := tx.Exec(ctx, targetMigration.SQL); err != nil {
			return fmt.Errorf("apply system migration %d (%s): %w", targetMigration.Version, targetMigration.Name, err)
		}
		executionMS = time.Since(migrationStarted).Milliseconds()
		if _, err := tx.Exec(ctx, `
			insert into pgbase_meta.system_migrations (version, name, checksum, execution_ms)
			values ($1, $2, $3, $4)`, targetMigration.Version, targetMigration.Name, targetMigration.SHA256[:], executionMS); err != nil {
			return fmt.Errorf("record system migration %d (%s): %w", targetMigration.Version, targetMigration.Name, err)
		}
		appliedTarget = true
		return nil
	})
	if err != nil {
		return receipt, err
	}
	if appliedTarget {
		r.logger.Info("applied system migration", "version", targetMigration.Version, "name", targetMigration.Name, "execution_ms", executionMS)
	}

	applied, present, err := readAppliedIfPresent(ctx, r.pool)
	if err != nil {
		return receipt, fmt.Errorf("read back system migration history: %w", err)
	}
	if !present {
		return receipt, fmt.Errorf("verify system migration target %d: committed history table is missing", target)
	}
	inspection := inspectHistory(r.migrations, applied)
	receipt.After = inspection.state
	if inspection.err != nil {
		return receipt, fmt.Errorf("verify system migration target %d: %w", target, inspection.err)
	}
	if inspection.issue != nil {
		return receipt, fmt.Errorf("verify system migration target %d: %w", target, inspection.issue)
	}
	if receipt.After.Version != target || receipt.After.Checksum != targetMigration.SHA256 {
		return receipt, fmt.Errorf("verify system migration target %d: committed history does not match target", target)
	}
	return receipt, nil
}

func exactTargetPrecondition(inspection historyInspection, target, previousVersion int64) error {
	if inspection.issue != nil {
		return inspection.issue
	}
	if inspection.state.Version != target && inspection.state.Version != previousVersion {
		return fmt.Errorf("system migration target %d requires current version %d or %d, got %d", target, target, previousVersion, inspection.state.Version)
	}
	return nil
}
