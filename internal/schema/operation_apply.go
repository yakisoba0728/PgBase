package schema

import (
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
	"github.com/webisnoteasy/PgBase/internal/observability"
)

func (s *Service) Apply(ctx context.Context, intent ApplyIntent) (result ApplyResult, err error) {
	operation, ok := schemaOperationForChange(intent.change)
	if !ok {
		return ApplyResult{}, ErrConflict
	}
	operationStarted := time.Now()
	relationOperation := changeHasRelationInput(intent.change)
	defer s.finishOperation(ctx, operation, operationStarted, &err)
	defer s.finishOperation(ctx, observability.OperationRelationSchema, operationStarted, &err, &relationOperation)
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
	// Which generation this preflight would issue does not matter: it executes
	// nothing and accepts both checksums, so it need not read the capability.
	plan, alternate, err := compileMixedFleetPlan(catalog, intent.change, false)
	if err != nil {
		return err
	}
	if plan.Checksum != intent.checksum && alternate.Checksum != intent.checksum {
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
	releaseConnection := func() {
		if connection != nil {
			connection.Release()
			connection = nil
		}
	}
	defer releaseConnection()

	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		releaseConnection()
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
		releaseConnection()
		return s.failClaimedOperation(claim, err)
	}

	commit := s.commitTx
	if commit == nil {
		commit = func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }
	}
	if err := commit(ctx, tx); err != nil {
		rollbackOperationTx(tx)
		txClosed = true
		releaseConnection()
		if schemaCommitDefinitelyFailed(err) {
			s.cache.discardPrepared(nextCatalog)
			return s.failClaimedOperation(claim, fmt.Errorf("commit schema operation: %w", err))
		}
		return s.resolveAmbiguousSchemaCommit(claim, result, nextCatalog)
	}
	txClosed = true
	releaseConnection()
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
	// D2-2 makes the capability row the only source of truth for the plan format this
	// binary issues. Reading it under the schema advisory lock this transaction holds
	// keeps the plan and the DDL below on one side of an activation or rollback.
	companions, err := companionCapabilityReady(ctx, tx)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	plan, alternate, err := compileMixedFleetPlan(catalog, intent.change, companions)
	if err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	claimed := hex.EncodeToString(claim.row.planChecksum[:])
	if (plan.Checksum != claimed && alternate.Checksum != claimed) ||
		plan.BaseVersion != claim.row.baseVersion || plan.Operation != string(claim.row.operation) {
		return ApplyResult{}, nil, operationRow{}, ErrConflict
	}
	// A checksum approves one statement list, not a change in the abstract. So when the
	// caller approved the other generation's plan, that plan is what runs: the DDL below,
	// the recorded plan JSON beside the caller's checksum, and whether companions got
	// built all follow it, not this binary's capability. Running the capability's plan
	// would apply DDL nobody approved and permanently belie the row's own checksum.
	executedCompanions := companions
	if plan.Checksum != claimed {
		plan, executedCompanions = alternate, !companions
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
		indexes, err := updatedIndexMetadata(current.Name, current.Indexes, plan.Definition)
		if err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
		result.Collection = Collection{
			ID: current.ID, Name: plan.Definition.Name, TableName: plan.Definition.TableName,
			Fields: fields, Indexes: indexes, CreatedAt: current.CreatedAt,
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
	started := time.Now()
	if intent.change.kind == changeUpdate {
		if err := validateChangedFieldOptions(ctx, tx, current, result.Collection); err != nil {
			return ApplyResult{}, nil, operationRow{}, err
		}
	}
	if err := s.executePlan(ctx, tx, plan); err != nil {
		return ApplyResult{}, nil, operationRow{}, err
	}
	switch intent.change.kind {
	case changeCreate:
		err = store.InsertCollection(ctx, result.Collection, plan.Definition, appliedAt)
	case changeUpdate:
		if err = validateChangedRecordSizes(ctx, tx, current, result.Collection); err == nil {
			err = store.UpdateCollection(ctx, result.Collection, plan.Definition, appliedAt)
		}
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
	// D2-4 splits catalog changes by whether they can leave a companion unbuilt, which is
	// a property of the manifest this transaction commits, not of the kind of change. The
	// exception is self-closing: a v2 create is the only plan that builds a companion, and
	// a create only adds its own table's, so it fills every gap it opens.
	builtItsOwn := executedCompanions && intent.change.kind == changeCreate
	if err := settleCompanionIndexCapability(ctx, tx, companions && (builtItsOwn ||
		!companionGapAfterChange(currentCatalog.expectedPhysicalSchema.companion,
			nextCatalog.expectedPhysicalSchema.companion)),
		newVersion, int64(len(nextCatalog.expectedPhysicalSchema.companion))); err != nil {
		return ApplyResult{}, nextCatalog, operationRow{}, err
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

// companionCapabilityReady reports whether this binary may issue the v2 plan format.
// A database predating migration 8 has no row, so v1.
//
// 'ready' alone is not enough: migration 8 made state the whole rule, but the generation
// it trusted named two compilers, so a row could be ready on a proof that proved nothing.
// The proved generation joins the gate; a carried capability issues v1 until re-proved.
func companionCapabilityReady(ctx context.Context, db database.DBTX) (bool, error) {
	status, err := ReadCompanionIndexStatus(ctx, db)
	if err != nil {
		return false, err
	}
	return schemaCompilerVersion >= 3 && status.Ready() && status.ProvenCompilerVersion == schemaCompilerVersion, nil
}

// companionGapAfterChange reports whether the committed manifest names a companion the
// previous one did not. That, not the kind of the change, is what 'ready' promises
// consumers they may require, so it alone has to close the gate. It is what lets an
// update naming no new companion — a rename, a no-op re-apply — carry the capability
// instead of costing a re-rollout, and it settles delete: a delete only removes entries.
func companionGapAfterChange(current, next []IndexSpec) bool {
	existing := make(map[string]struct{}, len(current))
	for _, companion := range current {
		existing[companionKey(companion)] = struct{}{}
	}
	for _, companion := range next {
		if _, present := existing[companionKey(companion)]; !present {
			return true
		}
	}
	return false
}

// settleCompanionIndexCapability closes the gate, or carries a ready capability onto
// the catalog this transaction commits: the promoted count and catalog version are part
// of the contract doctor checks, and a create or delete moves both. Either way it shares
// the DDL's transaction, so no reader sees a capability disagreeing with its catalog.
//
// updated_at is clock_timestamp(), not transaction_timestamp(): a promotion that committed
// while this transaction waited on the schema advisory lock wrote a ready_at later than
// this transaction began, and the v8 timeline check demands updated_at be at or after it.
// Demote is immune only because it nulls both timestamps on the way out.
func settleCompanionIndexCapability(
	ctx context.Context, tx pgx.Tx, advance bool, version, companions int64,
) error {
	if !advance {
		return demoteCompanionIndexCapability(ctx, tx)
	}
	if _, err := tx.Exec(ctx, `update pgbase_meta.schema_companion_index_capability
		set catalog_version = $1, ready_index_count = $2, updated_at = clock_timestamp()
		where singleton and state = 'ready_v3'`, version, companions); err != nil {
		return fmt.Errorf("advance schema companion index capability: %w", err)
	}
	return nil
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
	var postgresError *pgconn.PgError
	return errors.Is(err, pgx.ErrTxCommitRollback) || pgconn.SafeToRetry(err) || errors.As(err, &postgresError)
}

func newCollectionMetadataWithID(collectionID uuid.UUID, definition PlannedCollection) (Collection, error) {
	if collectionID == uuid.Nil {
		return Collection{}, ErrConflict
	}
	fields, err := updatedFieldMetadata(collectionID, nil, definition)
	if err != nil {
		return Collection{}, err
	}
	indexes, err := updatedIndexMetadata(definition.Name, nil, definition)
	if err != nil {
		return Collection{}, err
	}
	return Collection{
		ID: collectionID, Name: definition.Name, TableName: definition.TableName,
		Fields: fields, Indexes: indexes,
		View: resolvedViewSpec(definition.View),
	}, nil
}

func resolvedViewSpec(view *PlannedView) *ViewSpec {
	if view == nil {
		return nil
	}
	return &ViewSpec{SourceCollectionID: view.SourceCollectionID, Source: view.Source, Projection: append([]string(nil), view.Projection...), Filter: cloneViewFilter(view.Filter)}
}
