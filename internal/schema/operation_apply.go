package schema

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/webisnoteasy/PgBase/internal/database"
)

func (s *Service) Apply(ctx context.Context, intent ApplyIntent) (result ApplyResult, err error) {
	operation, ok := schemaOperationForChange(intent.change)
	if !ok {
		return ApplyResult{}, ErrConflict
	}
	operationStarted := time.Now()
	relationOperation := changeHasRelationInput(intent.change)
	defer s.finishOperation(ctx, operation, operationStarted, &err)
	defer s.finishRelationSchemaOperation(ctx, operationStarted, &err, &relationOperation)
	if err = s.ready(); err != nil {
		return ApplyResult{}, err
	}
	if intent.mode == operationModeLegacy {
		if err := s.preflightLegacyApply(ctx, intent); err != nil {
			return ApplyResult{}, err
		}
	}

	claim, err := s.claimOperation(ctx, intent)
	if err != nil {
		return ApplyResult{}, err
	}
	if !claim.acquired {
		return replayedApplyResult(claim.row)
	}
	return s.applyClaimedOperation(ctx, intent, claim, &relationOperation)
}

// Legacy callers historically learned about a stale version before request
// planning and about an absent collection before checksum comparison. Preserve
// that observable ordering before allocating a durable operation claim; the
// explicit apply transaction repeats every check under the schema locks.
func (s *Service) preflightLegacyApply(ctx context.Context, intent ApplyIntent) error {
	catalog, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	if catalog.Version != intent.baseVersion {
		return ErrConflict
	}
	plan, err := s.planner.Plan(catalog, intent.change)
	if err != nil {
		return err
	}
	if plan.Checksum != intent.checksum {
		return ErrConflict
	}
	return nil
}

func replayedApplyResult(row operationRow) (ApplyResult, error) {
	receipt := operationReceipt(row)
	result := ApplyResult{Receipt: receipt, Replayed: true}
	switch row.status {
	case OperationCommitted:
		return result, nil
	case OperationFailed:
		switch row.failureCode {
		case OperationFailurePlanConflict, OperationFailureLeaseLost:
			return result, ErrConflict
		case OperationFailureApplyFailed, OperationFailureReconcileFailed:
			return result, ErrOperationFailed
		default:
			return result, ErrOperationFailed
		}
	case OperationRunning, OperationUnknown:
		ref := OperationRef{actorID: row.actorID, operationID: row.operationID}
		return result, newOperationRecoveryError(ref, &receipt)
	default:
		return result, ErrOperationConflict
	}
}

func (s *Service) applyClaimedOperation(
	ctx context.Context,
	intent ApplyIntent,
	claim operationClaim,
	relationOperation *bool,
) (ApplyResult, error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return s.failClaimedOperation(claim, fmt.Errorf("acquire schema operation connection: %w", err))
	}
	defer connection.Release()

	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return s.failClaimedOperation(claim, fmt.Errorf("begin schema operation: %w", err))
	}
	txClosed := false
	defer func() {
		if !txClosed {
			rollbackOperationTx(tx)
		}
	}()

	result, nextCatalog, committedRow, err := s.executeClaimedOperation(ctx, tx, intent, claim, relationOperation)
	if err != nil {
		rollbackOperationTx(tx)
		txClosed = true
		s.cache.discardPrepared(nextCatalog)
		return s.failClaimedOperation(claim, err)
	}

	commit := s.commitTx
	if commit == nil {
		commit = func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }
	}
	if err := commit(ctx, tx); err != nil {
		rollbackOperationTx(tx)
		txClosed = true
		if schemaCommitDefinitelyFailed(err) {
			s.cache.discardPrepared(nextCatalog)
			return s.failClaimedOperation(claim, fmt.Errorf("commit schema operation: %w", err))
		}
		return s.resolveAmbiguousSchemaCommit(claim, result, nextCatalog)
	}
	txClosed = true
	result.Receipt = operationReceipt(committedRow)
	s.cache.publish(nextCatalog)
	return result, nil
}

func (s *Service) executeClaimedOperation(
	ctx context.Context,
	tx pgx.Tx,
	intent ApplyIntent,
	claim operationClaim,
	relationOperation *bool,
) (ApplyResult, *compiledCatalog, operationRow, error) {
	if _, err := tx.Exec(ctx, "set local lock_timeout = '5s'"); err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("set schema lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "set local statement_timeout = 0"); err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("disable request statement timeout for schema DDL: %w", err)
	}
	lockedClaim, err := s.lockOperationClaim(ctx, tx, claim)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	claim.row = lockedClaim
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaAdvisoryLockKey); err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("acquire schema advisory lock: %w", err)
	}

	store := NewStore(tx)
	version, err := store.LockedSchemaVersion(ctx)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	if version != claim.row.baseVersion || version != intent.baseVersion {
		return ApplyResult{}, nil, operationRow{}, ErrConflict
	}
	persisted, err := store.ReadCatalog(ctx, version)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	currentCatalog := s.cache.find(version)
	if currentCatalog == nil {
		currentCatalog, err = s.cache.prepare(persisted, s.catalogLimit())
		if err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
	} else if !sameCatalogContent(persisted, currentCatalog.catalog) {
		return ApplyResult{}, nil, operationRow{}, ErrConflict
	}
	catalog := currentCatalog.catalog
	plan, err := s.planner.Plan(catalog, intent.change)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	checksum, err := hex.DecodeString(plan.Checksum)
	if err != nil || len(checksum) != len(claim.row.planChecksum) ||
		!bytes.Equal(checksum, claim.row.planChecksum[:]) ||
		plan.BaseVersion != claim.row.baseVersion || plan.Operation != string(claim.row.operation) {
		return ApplyResult{}, nil, operationRow{}, ErrConflict
	}

	var result ApplyResult
	var current Collection
	switch intent.change.kind {
	case changeCreate:
		if err := validateCreateAgainstCatalog(catalog, plan.Definition); err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
		created, err := newCollectionMetadataWithID(claim.row.resourceID, plan.Definition)
		if err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
		result.Collection = created
	case changeUpdate:
		current = catalog.Collections[intent.change.update.Name]
		if current.ID == uuid.Nil || current.ID != claim.row.resourceID {
			return ApplyResult{}, nil, operationRow{}, ErrOperationConflict
		}
		if relationOperation != nil {
			*relationOperation = *relationOperation || collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
		}
		if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
		fields, err := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
		if err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
		result.Collection = Collection{
			ID: current.ID, Name: plan.Definition.Name, TableName: plan.Definition.TableName,
			Fields: fields, CreatedAt: current.CreatedAt,
		}
	case changeDelete:
		current = catalog.Collections[intent.change.deleteName]
		if current.ID == uuid.Nil || current.ID != claim.row.resourceID {
			return ApplyResult{}, nil, operationRow{}, ErrOperationConflict
		}
		if relationOperation != nil {
			*relationOperation = collectionHasRelationField(current) || len(catalog.Incoming[current.ID]) != 0
		}
		result.Deleted = true
	default:
		return ApplyResult{}, nil, operationRow{}, ErrConflict
	}

	var appliedAt time.Time
	if err := tx.QueryRow(ctx, "select current_timestamp").Scan(&appliedAt); err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("read schema application timestamp: %w", err)
	}
	appliedAt = appliedAt.UTC()
	if intent.change.kind == changeCreate {
		result.Collection.CreatedAt = appliedAt
	}
	// Claim this process's compiler version in the same transaction as the DDL.
	// A schema writer that never proves its version also never mutates the
	// catalog, so an activation reading unexpired leases sees every writer that
	// could still be holding an old plan.
	if err := s.refreshSchemaWriterLease(ctx, tx); err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	// The change can introduce a field whose companion index does not exist yet.
	// Closing the gate here, atomically with the catalog change, is what keeps a
	// ready capability from ever describing a catalog it has not been built for.
	if err := demoteCompanionIndexCapability(ctx, tx); err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	started := time.Now()
	if err := s.executePlan(ctx, tx, plan); err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	switch intent.change.kind {
	case changeCreate:
		err = store.InsertCollection(ctx, result.Collection, plan.Definition, appliedAt)
	case changeUpdate:
		err = store.UpdateCollection(ctx, result.Collection, plan.Definition, appliedAt)
	case changeDelete:
		err = store.DeleteCollection(ctx, current.ID)
	}
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, classifyApplyError(err)
	}

	newVersion := version + 1
	executionMS := time.Since(started).Milliseconds()
	if err := store.InsertMigration(
		ctx, newVersion, claim.row.operationID, plan, claim.row.planChecksum[:],
		claim.row.actorID, appliedAt, executionMS,
	); err != nil {
		return ApplyResult{}, nil, operationRow{}, classifyApplyError(err)
	}
	persistedVersion, err := store.IncrementVersion(ctx)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	if persistedVersion != newVersion {
		return ApplyResult{}, nil, operationRow{}, errors.New("schema version increment mismatch")
	}
	published, err := store.ReadCatalog(ctx, newVersion)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("build committed schema catalog: %w", err)
	}
	nextCatalog, err := s.cache.prepare(published, s.catalogLimit())
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, fmt.Errorf("compile committed schema catalog: %w", err)
	}
	var completedAt time.Time
	if err := tx.QueryRow(ctx, "select clock_timestamp()").Scan(&completedAt); err != nil {
		return ApplyResult{}, nextCatalog, operationRow{}, fmt.Errorf("read schema operation completion timestamp: %w", err)
	}
	committedRow, err := s.commitOperation(ctx, tx, claim, newVersion, completedAt.UTC())
	if err != nil {
		return ApplyResult{}, nextCatalog, operationRow{}, err
	}
	if _, err := tx.Exec(ctx, "select pg_notify('pgbase_schema_changed', $1)", strconv.FormatInt(newVersion, 10)); err != nil {
		return ApplyResult{}, nextCatalog, operationRow{}, fmt.Errorf("notify schema change: %w", err)
	}
	result.Receipt = operationReceipt(committedRow)
	return result, nextCatalog, committedRow, nil
}

func (s *Service) failClaimedOperation(claim operationClaim, cause error) (ApplyResult, error) {
	failureCode := OperationFailureApplyFailed
	switch {
	case errors.Is(cause, errOperationClaimLost):
		failureCode = OperationFailureLeaseLost
	case errors.Is(cause, ErrConflict):
		failureCode = OperationFailurePlanConflict
	}
	cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
	row, err := s.failOperation(cleanupCtx, claim, failureCode)
	cancel()
	if err != nil {
		receipt := operationReceipt(claim.row)
		ref := OperationRef{actorID: claim.row.actorID, operationID: claim.row.operationID}
		return ApplyResult{Receipt: receipt}, newOperationRecoveryError(ref, &receipt)
	}
	if row.status == OperationCommitted {
		return replayedApplyResult(row)
	}
	if row.status == OperationRunning || row.status == OperationUnknown {
		result, recoveryErr := replayedApplyResult(row)
		result.Replayed = false
		return result, recoveryErr
	}
	result := ApplyResult{Receipt: operationReceipt(row)}
	if failureCode == OperationFailureLeaseLost {
		return result, ErrConflict
	}
	return result, cause
}

func (s *Service) resolveAmbiguousSchemaCommit(
	claim operationClaim,
	committedResult ApplyResult,
	nextCatalog *compiledCatalog,
) (ApplyResult, error) {
	cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
	row, err := s.markOperationUnknown(cleanupCtx, claim)
	cancel()
	if err != nil {
		s.cache.discardPrepared(nextCatalog)
		receipt := operationReceipt(claim.row)
		ref := OperationRef{actorID: claim.row.actorID, operationID: claim.row.operationID}
		return ApplyResult{Receipt: receipt}, newOperationRecoveryError(ref, nil)
	}
	if row.status == OperationCommitted {
		committedResult.Receipt = operationReceipt(row)
		s.cache.publish(nextCatalog)
		return committedResult, nil
	}
	s.cache.discardPrepared(nextCatalog)
	result, terminalErr := replayedApplyResult(row)
	result.Replayed = false
	return result, terminalErr
}

func rollbackOperationTx(tx pgx.Tx) {
	cleanupCtx, cancel := database.BoundedCleanupContext(context.Background())
	defer cancel()
	_ = tx.Rollback(cleanupCtx)
}

func schemaCommitDefinitelyFailed(err error) bool {
	if errors.Is(err, pgx.ErrTxCommitRollback) || pgconn.SafeToRetry(err) {
		return true
	}
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError)
}

func newCollectionMetadataWithID(collectionID uuid.UUID, definition PlannedCollection) (Collection, error) {
	if collectionID == uuid.Nil {
		return Collection{}, ErrConflict
	}
	fields, err := newFieldMetadata(collectionID, definition)
	if err != nil {
		return Collection{}, err
	}
	return Collection{
		ID: collectionID, Name: definition.Name, TableName: definition.TableName,
		Fields: fields,
	}, nil
}
