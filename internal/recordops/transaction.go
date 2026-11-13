package recordops

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// compoundTx preserves batch atomicity inside a caller-owned transaction. Only
// the outer owner retries; a savepoint failure never commits earlier operations.
func (s *Service) compoundTx(ctx context.Context, operation func(context.Context, pgx.Tx) error) error {
	if tx, borrowed, err := s.pool.BorrowedTransaction(ctx); borrowed {
		if err != nil {
			return err
		}
		if err := requireTransactionOutcome(ctx); err != nil {
			return err
		}
		if err := database.RequireReadCommitted(ctx, tx); err != nil {
			return err
		}
		return database.InSavepoint(ctx, tx, func(savepoint pgx.Tx) error {
			return operation(WithTransaction(ctx, savepoint), savepoint)
		})
	}
	return s.ownedTx(ctx, pgx.ReadCommitted, func(tx pgx.Tx) error {
		owned := context.WithValue(WithTransaction(ctx, tx), managedTransactionKey{}, true)
		return operation(owned, tx)
	})
}

// createTx owns the idempotent-create order. The advisory lock is taken before
// EnsureCurrent performs the first snapshot-bearing catalog query, so a
// completed creator is either still holding the lock (409 in progress) or
// committed before this snapshot and therefore visible for replay.
func (s *Service) createTx(
	ctx context.Context,
	collectionName string,
	idempotencyLock *int64,
	operation func(pgx.Tx, schema.Catalog, schema.Collection) error,
) error {
	isolation := pgx.RepeatableRead
	if idempotencyLock != nil {
		// PostgreSQL establishes a snapshot before evaluating a SELECT-list
		// advisory-lock function. Read Committed makes the subsequent catalog and
		// replay lookup observe a creator that committed in that tiny interval.
		// The schema_state FOR SHARE lock still pins DDL for the whole transaction.
		isolation = pgx.ReadCommitted
	}
	return s.pinnedTx(ctx, collectionName, isolation, idempotencyLock, operation)
}

func (s *Service) updateTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.pinnedTx(ctx, collectionName, pgx.RepeatableRead, nil, operation)
}

// deleteTx owns the delete-graph order. EnsureCurrent takes schema_state FOR
// SHARE first; the graph then takes every participating collection advisory
// lock before reverse discovery. Read Committed makes a relation writer that
// committed while the graph waited visible to the first discovery statement,
// preventing a stale-snapshot phantom.
func (s *Service) deleteTx(ctx context.Context, collectionName string, operation func(pgx.Tx, schema.Catalog, schema.Collection) error) error {
	return s.pinnedTx(ctx, collectionName, pgx.ReadCommitted, nil, operation)
}

func (s *Service) pinnedTx(
	ctx context.Context,
	collectionName string,
	isolation pgx.TxIsoLevel,
	idempotencyLock *int64,
	operation func(pgx.Tx, schema.Catalog, schema.Collection) error,
) error {
	run := func(tx pgx.Tx) error {
		if idempotencyLock != nil {
			var locked bool
			if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", *idempotencyLock).Scan(&locked); err != nil {
				return err
			}
			if !locked {
				return &Error{Code: CodeIdempotencyInProgress}
			}
		}
		catalog, err := s.schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		collection, ok := catalog.Collections[collectionName]
		if !ok {
			return query.ErrCollectionNotFound
		}
		return operation(tx, catalog, collection)
	}
	if tx, borrowed, err := s.pool.BorrowedTransaction(ctx); borrowed {
		if err != nil {
			return err
		}
		if err := requireTransactionOutcome(ctx); err != nil {
			return err
		}
		if isolation == pgx.ReadCommitted {
			if err := database.RequireReadCommitted(ctx, tx); err != nil {
				return err
			}
		}
		if batched, _ := ctx.Value(batchMutationKey{}).(bool); batched {
			return run(tx)
		}
		// A validator or response projection can fail without aborting PostgreSQL.
		// Roll back this operation even if its owner later commits the transaction.
		return database.InSavepoint(ctx, tx, run)
	}
	return s.ownedTx(ctx, isolation, run)
}

func (s *Service) ownedTx(ctx context.Context, isolation pgx.TxIsoLevel, operation func(pgx.Tx) error) error {
	return database.Retry(ctx, func() error {
		if hooks, _ := ctx.Value(mutationHookKey{}).(*mutationHookRun); hooks != nil {
			hooks.events, hooks.failure = nil, nil
		}
		return s.pool.InTx(ctx, pgx.TxOptions{IsoLevel: isolation}, operation)
	}, func(cause database.RetryCause, exhausted bool) {
		s.record(ctx, observability.TransactionRetryEvent(cause, exhausted))
		if s.logger == nil {
			return
		}
		message := "retrying record operation"
		if exhausted {
			message = "record operation retries exhausted"
		}
		s.logger.WarnContext(ctx, message, "cause", cause)
	})
}
