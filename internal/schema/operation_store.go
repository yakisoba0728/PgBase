package schema

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errOperationClaimLost = errors.New("schema operation claim was lost")

type operationRow struct {
	operationID      uuid.UUID
	mode             operationMode
	actorID          uuid.UUID
	resourceID       uuid.UUID
	resourceKeyHash  [32]byte
	operation        SchemaOperation
	idempotencyHash  *[32]byte
	requestHash      [32]byte
	planChecksum     [32]byte
	baseVersion      int64
	status           OperationStatus
	fenceGeneration  int64
	leaseOwner       *uuid.UUID
	leaseExpiresAt   *time.Time
	reconcileAfter   *time.Time
	committedVersion *int64
	failureCode      OperationFailureCode
	createdAt        time.Time
	updatedAt        time.Time
	completedAt      *time.Time
}

type operationClaim struct {
	row             operationRow
	leaseOwner      uuid.UUID
	fenceGeneration int64
	acquired        bool
	replayed        bool
}

type operationClaimIdentity struct {
	operationID     uuid.UUID
	mode            operationMode
	actorID         uuid.UUID
	resourceKeyHash [32]byte
	operation       SchemaOperation
	idempotencyHash *[32]byte
	requestHash     [32]byte
	planChecksum    [32]byte
	baseVersion     int64
}

type foundOperationClaim struct {
	row              operationRow
	takeoverEligible bool
	requestedIDMatch bool
}

const operationRowColumns = `operation_id, mode, actor_id, resource_id,
	resource_key_hash, operation, idempotency_key_hash, request_hash,
	plan_checksum, base_version, status, fence_generation, lease_owner,
	lease_expires_at, reconcile_after, committed_version, failure_code,
	created_at, updated_at, completed_at`

func (s *Service) claimOperation(ctx context.Context, intent ApplyIntent) (operationClaim, error) {
	identity, err := newOperationClaimIdentity(intent)
	if err != nil {
		return operationClaim{}, err
	}
	leaseOwner, err := uuid.NewV7()
	if err != nil {
		return operationClaim{}, fmt.Errorf("generate schema operation lease owner: %w", err)
	}
	claim := operationClaim{leaseOwner: leaseOwner}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if identity.mode == operationModeGuarded {
			existing, err := findOperationClaim(ctx, tx, identity)
			if err != nil {
				return err
			}
			if existing != nil {
				claimed, err := takeOrReplayOperation(ctx, tx, identity, leaseOwner, *existing)
				if err != nil {
					return err
				}
				claim = claimed
				return nil
			}
			auditExists, err := schemaOperationAuditIDExists(ctx, tx, identity.operationID)
			if err != nil {
				return err
			}
			if auditExists {
				return ErrOperationConflict
			}
		}

		resourceID, err := resolveNewOperationResource(ctx, tx, intent.change)
		if err != nil {
			return err
		}
		inserted, err := insertOperationClaim(ctx, tx, identity, resourceID, leaseOwner)
		if err != nil {
			return err
		}
		if inserted != nil {
			claim = acquiredOperationClaim(*inserted, leaseOwner)
			return nil
		}
		existing, err := findOperationClaim(ctx, tx, identity)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("claim schema operation: conflict row disappeared")
		}
		claimed, err := takeOrReplayOperation(ctx, tx, identity, leaseOwner, *existing)
		if err != nil {
			return err
		}
		claim = claimed
		return nil
	})
	if err != nil {
		return operationClaim{}, err
	}
	return claim, nil
}

func schemaOperationAuditIDExists(ctx context.Context, tx pgx.Tx, operationID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pgbase_meta.schema_migrations WHERE operation_id = $1
	)`, operationID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check schema operation audit identity: %w", err)
	}
	return exists, nil
}

func newOperationClaimIdentity(intent ApplyIntent) (operationClaimIdentity, error) {
	if intent.actor == uuid.Nil || intent.baseVersion < 0 {
		if intent.mode == operationModeLegacy {
			return operationClaimIdentity{}, ErrConflict
		}
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	operation, ok := schemaOperation(intent.change)
	if !ok {
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	checksum, err := hex.DecodeString(intent.checksum)
	if err != nil || len(checksum) != 32 {
		if intent.mode == operationModeLegacy {
			return operationClaimIdentity{}, ErrConflict
		}
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	resourceKeyHash, err := schemaResourceKeyHash(intent.change)
	if err != nil {
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	requestHash, err := schemaRequestHash(intent.change)
	if err != nil {
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	identity := operationClaimIdentity{
		mode: intent.mode, actorID: intent.actor, operation: operation,
		resourceKeyHash: resourceKeyHash, requestHash: requestHash,
		baseVersion: intent.baseVersion,
	}
	copy(identity.planChecksum[:], checksum)
	switch intent.mode {
	case operationModeLegacy:
		operationID, err := uuid.NewV7()
		if err != nil {
			return operationClaimIdentity{}, fmt.Errorf("generate schema operation ID: %w", err)
		}
		identity.operationID = operationID
	case operationModeGuarded:
		if intent.operationID == uuid.Nil || intent.resourceKeyHash != resourceKeyHash ||
			intent.requestHash != requestHash || intent.planChecksum != identity.planChecksum {
			return operationClaimIdentity{}, ErrInvalidOperation
		}
		identity.operationID = intent.operationID
		idempotencyHash := intent.idempotencyKeyHash
		identity.idempotencyHash = &idempotencyHash
	default:
		return operationClaimIdentity{}, ErrInvalidOperation
	}
	return identity, nil
}

func resolveNewOperationResource(ctx context.Context, tx pgx.Tx, change Change) (uuid.UUID, error) {
	if change.kind == changeCreate {
		resourceID, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, fmt.Errorf("generate schema operation resource ID: %w", err)
		}
		return resourceID, nil
	}
	name, ok := schemaOperationResourceName(change)
	if !ok {
		return uuid.Nil, ErrInvalidOperation
	}
	var resourceID uuid.UUID
	if err := tx.QueryRow(ctx, `select id from pgbase_meta.collections where name = $1`, name).Scan(&resourceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrCollectionNotFound
		}
		return uuid.Nil, fmt.Errorf("resolve schema operation collection: %w", err)
	}
	return resourceID, nil
}

func findOperationClaim(ctx context.Context, tx pgx.Tx, identity operationClaimIdentity) (*foundOperationClaim, error) {
	idempotencyHash := operationIdempotencyBytes(identity.idempotencyHash)
	rows, err := tx.Query(ctx, `select `+operationRowColumns+`,
			((status = 'running' and lease_expires_at <= current_timestamp)
				or (status = 'unknown' and reconcile_after <= current_timestamp)),
			(operation_id = $1)
		from pgbase_meta.schema_operations
		where operation_id = $1
			or ($2::boolean
				and mode = 'guarded'
				and actor_kind = 'administrator'
				and actor_id = $3
				and resource_kind = 'collection'
				and resource_key_hash = $4
				and idempotency_key_hash = $5)
		order by (operation_id = $1) desc
		limit 2`,
		identity.operationID, identity.mode == operationModeGuarded, identity.actorID,
		identity.resourceKeyHash[:], idempotencyHash,
	)
	if err != nil {
		return nil, fmt.Errorf("find schema operation claim: %w", err)
	}
	defer rows.Close()
	var found []foundOperationClaim
	for rows.Next() {
		var takeoverEligible bool
		var requestedIDMatch bool
		row, err := scanOperationRowWithExtras(rows, &takeoverEligible, &requestedIDMatch)
		if err != nil {
			return nil, fmt.Errorf("read schema operation claim: %w", err)
		}
		found = append(found, foundOperationClaim{
			row: row, takeoverEligible: takeoverEligible, requestedIDMatch: requestedIDMatch,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema operation claim: %w", err)
	}
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) != 1 {
		return nil, ErrOperationConflict
	}
	match := found[0]
	if !match.requestedIDMatch || match.row.operationID != identity.operationID ||
		!operationIdentityWithoutIDMatches(match.row, identity) {
		return nil, ErrOperationConflict
	}
	return &found[0], nil
}

func insertOperationClaim(
	ctx context.Context,
	tx pgx.Tx,
	identity operationClaimIdentity,
	resourceID uuid.UUID,
	leaseOwner uuid.UUID,
) (*operationRow, error) {
	row, err := scanOperationRow(tx.QueryRow(ctx, `insert into pgbase_meta.schema_operations (
			operation_id, mode, actor_kind, actor_id, resource_kind, resource_id,
			resource_key_hash, operation, idempotency_key_hash, request_hash,
			plan_checksum, base_version, status, fence_generation, lease_owner,
			lease_expires_at, reconcile_after, created_at, updated_at
		) values (
			$1, $2, 'administrator', $3, 'collection', $4,
			$5, $6, $7, $8, $9, $10, 'running', 1, $11,
			current_timestamp + interval '2 minutes',
			current_timestamp + interval '2 minutes 10 seconds',
			current_timestamp, current_timestamp
		)
		on conflict do nothing
		returning `+operationRowColumns,
		identity.operationID, identity.mode, identity.actorID, resourceID,
		identity.resourceKeyHash[:], string(identity.operation), operationIdempotencyBytes(identity.idempotencyHash),
		identity.requestHash[:], identity.planChecksum[:], identity.baseVersion, leaseOwner,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("insert schema operation claim: %w", err)
	}
	refreshed, err := refreshAcquiredOperationClaim(ctx, tx, row, leaseOwner, true)
	if err != nil {
		return nil, err
	}
	return &refreshed, nil
}

func takeOrReplayOperation(
	ctx context.Context,
	tx pgx.Tx,
	identity operationClaimIdentity,
	leaseOwner uuid.UUID,
	current foundOperationClaim,
) (operationClaim, error) {
	if current.row.status == OperationCommitted || current.row.status == OperationFailed || !current.takeoverEligible {
		return replayedOperationClaim(current.row), nil
	}
	row, err := scanOperationRow(tx.QueryRow(ctx, `update pgbase_meta.schema_operations
		set status = 'running',
			fence_generation = fence_generation + 1,
			lease_owner = $2,
			updated_at = current_timestamp,
			lease_expires_at = current_timestamp + interval '2 minutes',
			reconcile_after = current_timestamp + interval '2 minutes 10 seconds'
		where operation_id = $1
			and ((status = 'running' and lease_expires_at <= current_timestamp)
				or (status = 'unknown' and reconcile_after <= current_timestamp))
		returning `+operationRowColumns, current.row.operationID, leaseOwner))
	if err == nil {
		row, err = refreshAcquiredOperationClaim(ctx, tx, row, leaseOwner, false)
		if err != nil {
			return operationClaim{}, err
		}
		if !operationIdentityWithoutIDMatches(row, identity) || !operationRowsShareIdentity(row, current.row) {
			return operationClaim{}, ErrOperationConflict
		}
		return acquiredOperationClaim(row, leaseOwner), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return operationClaim{}, fmt.Errorf("take over schema operation claim: %w", err)
	}
	refreshed, err := findOperationClaim(ctx, tx, identity)
	if err != nil {
		return operationClaim{}, err
	}
	if refreshed == nil {
		return operationClaim{}, fmt.Errorf("replay schema operation claim: row disappeared")
	}
	return replayedOperationClaim(refreshed.row), nil
}

func refreshAcquiredOperationClaim(
	ctx context.Context,
	tx pgx.Tx,
	row operationRow,
	leaseOwner uuid.UUID,
	resetCreatedAt bool,
) (operationRow, error) {
	refreshed, err := scanOperationRow(tx.QueryRow(ctx, `WITH lease_clock AS MATERIALIZED (
			SELECT clock_timestamp() AS acquired_at
		)
		UPDATE pgbase_meta.schema_operations AS operation
		SET created_at = CASE WHEN $5 THEN lease_clock.acquired_at ELSE operation.created_at END,
			updated_at = lease_clock.acquired_at,
			lease_expires_at = lease_clock.acquired_at + interval '2 minutes',
			reconcile_after = lease_clock.acquired_at + interval '2 minutes 10 seconds'
		FROM lease_clock
		WHERE operation.operation_id = $1
			AND operation.status = 'running'
			AND operation.lease_owner = $2
			AND operation.fence_generation = $3
			AND operation.request_hash = $4
		RETURNING `+operationRowColumns,
		row.operationID, leaseOwner, row.fenceGeneration, row.requestHash[:], resetCreatedAt,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return operationRow{}, errOperationClaimLost
		}
		return operationRow{}, fmt.Errorf("refresh schema operation claim lease: %w", err)
	}
	return refreshed, nil
}

func acquiredOperationClaim(row operationRow, leaseOwner uuid.UUID) operationClaim {
	return operationClaim{
		row: row, leaseOwner: leaseOwner, fenceGeneration: row.fenceGeneration,
		acquired: true,
	}
}

func replayedOperationClaim(row operationRow) operationClaim {
	return operationClaim{row: row, fenceGeneration: row.fenceGeneration, replayed: true}
}

func operationIdentityWithoutIDMatches(row operationRow, identity operationClaimIdentity) bool {
	return row.mode == identity.mode && row.actorID == identity.actorID && row.resourceKeyHash == identity.resourceKeyHash &&
		row.operation == identity.operation && pointerEqual(row.idempotencyHash, identity.idempotencyHash) &&
		row.requestHash == identity.requestHash && row.planChecksum == identity.planChecksum &&
		row.baseVersion == identity.baseVersion
}

func operationIdempotencyBytes(value *[32]byte) []byte {
	if value == nil {
		return nil
	}
	return value[:]
}

func scanOperationRow(scanner pgx.Row) (operationRow, error) {
	return scanOperationRowWithExtras(scanner)
}

func scanOperationRowWithExtras(scanner pgx.Row, extras ...any) (operationRow, error) {
	var row operationRow
	var mode string
	var operation string
	var status string
	var resourceKeyHash []byte
	var idempotencyHash []byte
	var requestHash []byte
	var planChecksum []byte
	var leaseOwner pgtype.UUID
	var leaseExpiresAt pgtype.Timestamptz
	var reconcileAfter pgtype.Timestamptz
	var committedVersion pgtype.Int8
	var failureCode pgtype.Text
	var completedAt pgtype.Timestamptz
	destinations := []any{
		&row.operationID, &mode, &row.actorID, &row.resourceID,
		&resourceKeyHash, &operation, &idempotencyHash, &requestHash,
		&planChecksum, &row.baseVersion, &status, &row.fenceGeneration, &leaseOwner,
		&leaseExpiresAt, &reconcileAfter, &committedVersion, &failureCode,
		&row.createdAt, &row.updatedAt, &completedAt,
	}
	destinations = append(destinations, extras...)
	if err := scanner.Scan(destinations...); err != nil {
		return operationRow{}, err
	}
	parsedMode := operationMode(mode)
	if (parsedMode != operationModeLegacy && parsedMode != operationModeGuarded) ||
		!validStoredOperation(SchemaOperation(operation)) || !validStoredOperationStatus(OperationStatus(status)) ||
		len(resourceKeyHash) != 32 || len(requestHash) != 32 || len(planChecksum) != 32 ||
		(parsedMode == operationModeLegacy && idempotencyHash != nil) ||
		(parsedMode == operationModeGuarded && len(idempotencyHash) != 32) {
		return operationRow{}, errors.New("invalid persisted schema operation")
	}
	row.mode = parsedMode
	row.operation = SchemaOperation(operation)
	row.status = OperationStatus(status)
	copy(row.resourceKeyHash[:], resourceKeyHash)
	copy(row.requestHash[:], requestHash)
	copy(row.planChecksum[:], planChecksum)
	if idempotencyHash != nil {
		var hash [32]byte
		copy(hash[:], idempotencyHash)
		row.idempotencyHash = &hash
	}
	if leaseOwner.Valid {
		value := uuid.UUID(leaseOwner.Bytes)
		row.leaseOwner = &value
	}
	if leaseExpiresAt.Valid {
		value := leaseExpiresAt.Time.UTC()
		row.leaseExpiresAt = &value
	}
	if reconcileAfter.Valid {
		value := reconcileAfter.Time.UTC()
		row.reconcileAfter = &value
	}
	if committedVersion.Valid {
		value := committedVersion.Int64
		row.committedVersion = &value
	}
	if failureCode.Valid {
		if !validOperationFailureCode(OperationFailureCode(failureCode.String)) {
			return operationRow{}, errors.New("invalid persisted schema operation")
		}
		row.failureCode = OperationFailureCode(failureCode.String)
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		row.completedAt = &value
	}
	row.createdAt = row.createdAt.UTC()
	row.updatedAt = row.updatedAt.UTC()
	return row, nil
}

func validStoredOperation(operation SchemaOperation) bool {
	return operation == SchemaOperationCollectionCreate ||
		operation == SchemaOperationCollectionUpdate ||
		operation == SchemaOperationCollectionDelete
}

func validStoredOperationStatus(status OperationStatus) bool {
	return status == OperationRunning || status == OperationUnknown ||
		status == OperationCommitted || status == OperationFailed
}

func (s *Service) lockOperationClaim(ctx context.Context, tx pgx.Tx, claim operationClaim) (operationRow, error) {
	row, err := scanOperationRow(tx.QueryRow(ctx, `select `+operationRowColumns+`
		from pgbase_meta.schema_operations where operation_id = $1 for update`, claim.row.operationID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return operationRow{}, errOperationClaimLost
		}
		return operationRow{}, fmt.Errorf("lock schema operation claim: %w", err)
	}
	if !operationRowsShareIdentity(row, claim.row) || !operationClaimTokenMatches(row, claim) {
		return operationRow{}, errOperationClaimLost
	}
	return row, nil
}

func (s *Service) commitOperation(
	ctx context.Context,
	tx pgx.Tx,
	claim operationClaim,
	committedVersion int64,
	completedAt time.Time,
) (operationRow, error) {
	if committedVersion != claim.row.baseVersion+1 || completedAt.IsZero() {
		return operationRow{}, ErrInvalidOperation
	}
	row, err := scanOperationRow(tx.QueryRow(ctx, `update pgbase_meta.schema_operations
		set status = 'committed', committed_version = $5,
			lease_owner = null, lease_expires_at = null, reconcile_after = null,
			updated_at = $6, completed_at = $6
		where operation_id = $1 and status = 'running' and lease_owner = $2
			and fence_generation = $3 and request_hash = $4
		returning `+operationRowColumns,
		claim.row.operationID, claim.leaseOwner, claim.fenceGeneration, claim.row.requestHash[:],
		committedVersion, completedAt.UTC(),
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return operationRow{}, errOperationClaimLost
		}
		return operationRow{}, fmt.Errorf("commit schema operation receipt: %w", err)
	}
	return row, nil
}

func (s *Service) failOperation(ctx context.Context, claim operationClaim, failureCode OperationFailureCode) (operationRow, error) {
	if !validOperationFailureCode(failureCode) {
		return operationRow{}, ErrInvalidOperation
	}
	return s.followUpOperationTransition(ctx, claim, `update pgbase_meta.schema_operations
		set status = 'failed', failure_code = $5,
			lease_owner = null, lease_expires_at = null, reconcile_after = null,
			updated_at = current_timestamp, completed_at = current_timestamp
		where operation_id = $1 and status = 'running' and lease_owner = $2
			and fence_generation = $3 and request_hash = $4
		returning `+operationRowColumns, failureCode)
}

func (s *Service) markOperationUnknown(ctx context.Context, claim operationClaim) (operationRow, error) {
	return s.followUpOperationTransition(ctx, claim, `update pgbase_meta.schema_operations
		set status = 'unknown', failure_code = null,
			lease_owner = null, lease_expires_at = null,
			updated_at = current_timestamp,
			reconcile_after = current_timestamp + interval '10 seconds'
		where operation_id = $1 and status = 'running' and lease_owner = $2
			and fence_generation = $3 and request_hash = $4
		returning `+operationRowColumns, nil)
}

func (s *Service) followUpOperationTransition(
	ctx context.Context,
	claim operationClaim,
	statement string,
	fifthArgument any,
) (operationRow, error) {
	var transitioned operationRow
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		arguments := []any{
			claim.row.operationID, claim.leaseOwner, claim.fenceGeneration,
			claim.row.requestHash[:],
		}
		if fifthArgument != nil {
			arguments = append(arguments, fifthArgument)
		}
		row, err := scanOperationRow(tx.QueryRow(ctx, statement, arguments...))
		if err == nil {
			transitioned = row
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := readOwnedOperationWith(ctx, tx, claim.row.actorID, claim.row.operationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errOperationClaimLost
			}
			return err
		}
		if !operationRowsShareIdentity(current, claim.row) ||
			(current.status == OperationRunning && !operationClaimTokenMatches(current, claim)) {
			return errOperationClaimLost
		}
		transitioned = current
		return nil
	})
	if err != nil {
		if errors.Is(err, errOperationClaimLost) {
			return operationRow{}, err
		}
		return operationRow{}, fmt.Errorf("transition schema operation receipt: %w", err)
	}
	return transitioned, nil
}

func readOwnedOperationWith(ctx context.Context, tx pgx.Tx, actorID, operationID uuid.UUID) (operationRow, error) {
	row, err := scanOperationRow(tx.QueryRow(ctx, `select `+operationRowColumns+`
		from pgbase_meta.schema_operations
		where actor_kind = 'administrator' and actor_id = $1 and operation_id = $2`, actorID, operationID))
	if err != nil {
		return operationRow{}, err
	}
	return row, nil
}
func operationRowsShareIdentity(left, right operationRow) bool {
	return left.operationID == right.operationID && left.mode == right.mode &&
		left.actorID == right.actorID && left.resourceID == right.resourceID &&
		left.resourceKeyHash == right.resourceKeyHash && left.operation == right.operation &&
		pointerEqual(left.idempotencyHash, right.idempotencyHash) &&
		left.requestHash == right.requestHash && left.planChecksum == right.planChecksum &&
		left.baseVersion == right.baseVersion
}

func operationClaimTokenMatches(row operationRow, claim operationClaim) bool {
	return row.status == OperationRunning && row.leaseOwner != nil &&
		*row.leaseOwner == claim.leaseOwner && row.fenceGeneration == claim.fenceGeneration
}

func validOperationFailureCode(code OperationFailureCode) bool {
	return code == OperationFailurePlanConflict || code == OperationFailureApplyFailed ||
		code == OperationFailureLeaseLost || code == OperationFailureReconcileFailed
}
