package schema

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	defaultOperationPageLimit           = 20
	maximumOperationPageLimit           = 100
	maximumOperationPageBytes           = 256 * 1024
	minimumOperationIdempotencyKeyBytes = 16
	maximumOperationIdempotencyKeyBytes = 128
	maximumOperationCursorEncodedBytes  = 512
)

var (
	ErrInvalidOperation        = errors.New("invalid schema operation")
	ErrOperationNotFound       = errors.New("schema operation does not exist")
	ErrOperationConflict       = fmt.Errorf("%w: schema operation identity", ErrConflict)
	ErrOperationInProgress     = errors.New("schema operation is in progress")
	ErrOperationOutcomeUnknown = errors.New("schema operation outcome is unknown")
	ErrOperationFailed         = errors.New("schema operation failed")
)

type operationMode string

const (
	operationModeLegacy  operationMode = "legacy"
	operationModeGuarded operationMode = "guarded"
)

// SchemaOperation is the exact immutable operation vocabulary shared by the
// operation ledger and schema migration audit.
type SchemaOperation string

const (
	SchemaOperationCollectionCreate SchemaOperation = "collection_create"
	SchemaOperationCollectionUpdate SchemaOperation = "collection_update"
	SchemaOperationCollectionDelete SchemaOperation = "collection_delete"
)

type OperationStatus string

const (
	OperationRunning   OperationStatus = "running"
	OperationUnknown   OperationStatus = "unknown"
	OperationCommitted OperationStatus = "committed"
	OperationFailed    OperationStatus = "failed"
)

// OperationFailureCode is a bounded terminal classification. Raw database,
// SQL, request, and error text are never part of a receipt.
type OperationFailureCode string

const (
	OperationFailurePlanConflict    OperationFailureCode = "plan_conflict"
	OperationFailureApplyFailed     OperationFailureCode = "apply_failed"
	OperationFailureLeaseLost       OperationFailureCode = "lease_lost"
	OperationFailureReconcileFailed OperationFailureCode = "reconcile_failed"
)

// OperationReceipt is the redacted, owner-scoped view of one durable schema
// operation. Zero committed/failure values mean that state has not been set.
type OperationReceipt struct {
	OperationID      uuid.UUID            `json:"operationId"`
	Operation        SchemaOperation      `json:"operation"`
	CollectionID     uuid.UUID            `json:"collectionId"`
	Status           OperationStatus      `json:"status"`
	BaseVersion      int64                `json:"baseVersion"`
	CommittedVersion int64                `json:"committedVersion,omitempty"`
	FailureCode      OperationFailureCode `json:"failureCode,omitempty"`
	CreatedAt        time.Time            `json:"createdAt"`
	UpdatedAt        time.Time            `json:"updatedAt"`
	CompletedAt      *time.Time           `json:"completedAt,omitempty"`
}

// OperationPage is a bounded owner-scoped history page ordered by
// (created_at DESC, operation_id DESC).
type OperationPage struct {
	Items      []OperationReceipt `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

// OperationRef can only be constructed with both the requested operation and
// the current actor scope.
type OperationRef struct {
	actorID     uuid.UUID
	operationID uuid.UUID
}

// ID returns the client-known operation ID without exposing its owner scope.
func (r OperationRef) ID() uuid.UUID { return r.operationID }

func NewOperationRef(actorID, operationID uuid.UUID) (OperationRef, error) {
	if actorID == uuid.Nil || operationID == uuid.Nil {
		return OperationRef{}, ErrInvalidOperation
	}
	return OperationRef{actorID: actorID, operationID: operationID}, nil
}

// OperationQuery can only be constructed with an owner scope and bounded
// fixed-order pagination inputs. A zero limit selects the default of 20.
type OperationQuery struct {
	actorID uuid.UUID
	limit   int
	cursor  string
}

func NewOperationQuery(actorID uuid.UUID, limit int, cursor string) (OperationQuery, error) {
	if actorID == uuid.Nil || limit < 0 || limit > maximumOperationPageLimit || len(cursor) > maximumOperationCursorEncodedBytes {
		return OperationQuery{}, ErrInvalidOperation
	}
	if limit == 0 {
		limit = defaultOperationPageLimit
	}
	return OperationQuery{actorID: actorID, limit: limit, cursor: cursor}, nil
}

// OperationRecoveryError tells a caller to recover through OperationStatus.
// Its actor scope and optional receipt remain sealed domain values.
type OperationRecoveryError struct {
	ref        OperationRef
	receipt    OperationReceipt
	hasReceipt bool
}

func newOperationRecoveryError(ref OperationRef, receipt *OperationReceipt) *OperationRecoveryError {
	recovery := &OperationRecoveryError{ref: ref}
	if receipt != nil {
		recovery.receipt = *receipt
		recovery.hasReceipt = true
	}
	return recovery
}

func (e *OperationRecoveryError) Error() string { return "schema operation requires recovery" }

func (e *OperationRecoveryError) Unwrap() error {
	if e != nil && e.hasReceipt && e.receipt.Status == OperationRunning {
		return ErrOperationInProgress
	}
	return ErrOperationOutcomeUnknown
}

func (e *OperationRecoveryError) Receipt() (OperationReceipt, bool) {
	if e == nil || !e.hasReceipt {
		return OperationReceipt{}, false
	}
	return e.receipt, true
}

// GuardedApply binds a client-known operation and bounded idempotency identity
// to one canonical typed request. Callers cannot supply any persisted hash.
// Retries must reuse both the operation ID and idempotency key; changing either
// is a conflict.
func GuardedApply(change Change, expectedPlan Plan, actor, clientOperationID uuid.UUID, idempotencyKey string) (ApplyIntent, error) {
	if actor == uuid.Nil || clientOperationID == uuid.Nil || expectedPlan.BaseVersion < 0 || !validOperationIdempotencyKey(idempotencyKey) {
		return ApplyIntent{}, ErrInvalidOperation
	}
	checksum, err := hex.DecodeString(expectedPlan.Checksum)
	if err != nil || len(checksum) != 32 {
		return ApplyIntent{}, ErrInvalidOperation
	}
	resourceKeyHash, err := schemaResourceKeyHash(change)
	if err != nil {
		return ApplyIntent{}, fmt.Errorf("%w: request identity", ErrInvalidOperation)
	}
	requestHash, err := schemaRequestHash(change)
	if err != nil {
		return ApplyIntent{}, fmt.Errorf("%w: request identity", ErrInvalidOperation)
	}
	var planChecksum [32]byte
	copy(planChecksum[:], checksum)
	return ApplyIntent{
		mode: operationModeGuarded, change: change,
		checksum: expectedPlan.Checksum, baseVersion: expectedPlan.BaseVersion,
		actor: actor, operationID: clientOperationID,
		resourceKeyHash:    resourceKeyHash,
		idempotencyKeyHash: schemaIdempotencyKeyHash(idempotencyKey),
		requestHash:        requestHash, planChecksum: planChecksum,
	}, nil
}

func validOperationIdempotencyKey(value string) bool {
	if len(value) < minimumOperationIdempotencyKeyBytes || len(value) > maximumOperationIdempotencyKeyBytes {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '~' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func schemaOperation(change Change) (SchemaOperation, bool) {
	switch change.kind {
	case changeCreate:
		return SchemaOperationCollectionCreate, true
	case changeUpdate:
		return SchemaOperationCollectionUpdate, true
	case changeDelete:
		return SchemaOperationCollectionDelete, true
	default:
		return "", false
	}
}
