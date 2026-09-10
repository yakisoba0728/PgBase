// Package recordops owns the principal-aware transaction boundary for record
// reads and mutations. Callers provide authenticated identity and bounded HTTP
// metadata; the package keeps record, revision, audit, outbox, and idempotency
// state atomic in PostgreSQL.
package recordops

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
)

type PrincipalKind = identity.Kind

const PrincipalAdministrator PrincipalKind = identity.Administrator
const PrincipalUser PrincipalKind = identity.User

type Principal = identity.Subject

type EntityTagSet struct {
	Present bool
	Any     bool
	Values  []string
}

type Compatibility string

const (
	CompatibilityNone                  Compatibility = ""
	CompatibilityMissingIdempotencyKey Compatibility = "missing-idempotency-key"
	CompatibilityLastWriteWins         Compatibility = "missing-if-match"
)

type Record = query.Record

type MutationResult struct {
	Record        *Record
	EventID       uuid.UUID
	InternalID    uuid.UUID
	Replayed      bool
	Compatibility Compatibility
}

type ListRequest struct {
	Principal               Principal
	RequestID               string
	Collection              string
	Cursor                  string
	Limit                   int
	Filter                  string
	Sort                    string
	Expand                  string
	Fields                  string
	PocketBaseCompatibility bool
	RuleContext             *accessrule.RequestContext
}

type GetRequest struct {
	Principal               Principal
	RequestID               string
	Collection              string
	ID                      uuid.UUID
	PublicID                string
	Expand                  string
	Fields                  string
	PocketBaseCompatibility bool
	RuleContext             *accessrule.RequestContext
}

type CreateRequest struct {
	Principal          Principal
	RequestID          string
	Collection         string
	Input              map[string]json.RawMessage
	IdempotencyKey     string
	ID                 uuid.UUID
	IdempotencyContext []byte
	RuleContext        *accessrule.RequestContext
}

type UpdateRequest struct {
	Principal      Principal
	RequestID      string
	Collection     string
	ID             uuid.UUID
	PublicID       string
	Input          map[string]json.RawMessage
	IfMatch        EntityTagSet
	SidecarFields  []string
	SidecarContext []byte
	RuleContext    *accessrule.RequestContext
}

type DeleteRequest struct {
	Principal   Principal
	RequestID   string
	Collection  string
	ID          uuid.UUID
	PublicID    string
	IfMatch     EntityTagSet
	RuleContext *accessrule.RequestContext
}

// ValidateRequest validates a proposed record without writing it. An empty ID
// applies create semantics; a non-empty ID applies patch semantics.
type ValidateRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
	ID         uuid.UUID
	PublicID   string
	Input      map[string]json.RawMessage
}

// BatchRequest is an atomic, ordered group of record mutations. Batch is
// intentionally limited at the API boundary to 50 operations and one MiB of
// decoded JSON; callers should not use it for bulk import workloads.
type BatchRequest struct {
	Principal    Principal
	RequestID    string
	Operations   []BatchOperation
	Participants []BatchParticipant
	RuleContext  *accessrule.RequestContext
}

// BatchParticipant joins sidecar state to the same transaction after all
// ordered record mutations have produced their final record IDs.
type BatchParticipant interface {
	CommitBatch(context.Context, pgx.Tx, []MutationResult) error
}

// TransactionValidator checks sidecar invariants after every participant has
// written its final state and before the record transaction commits.
type TransactionValidator interface {
	ValidateRecords(context.Context, pgx.Tx, BatchRequest, []MutationResult) error
}

type BatchOperation struct {
	Create *CreateRequest
	Update *UpdateRequest
	Delete *DeleteRequest
	Upsert *UpdateRequest
}

func (operation BatchOperation) kind() string {
	switch {
	case operation.Create != nil:
		return "create"
	case operation.Update != nil:
		return "update"
	case operation.Delete != nil:
		return "delete"
	case operation.Upsert != nil:
		return "upsert"
	default:
		return ""
	}
}

type RecordPage = query.RecordPage
type PocketBasePage = query.PocketBasePage
type PocketBaseListRequest struct {
	ListRequest
	Page      int
	PerPage   int
	SkipTotal bool
}

type ErrorCode string

const (
	CodeInvalidPrincipal      ErrorCode = "invalid_principal"
	CodeInvalidRequest        ErrorCode = "invalid_request_metadata"
	CodeInvalidIdempotencyKey ErrorCode = "invalid_idempotency_key"
	CodeIdempotencyConflict   ErrorCode = "idempotency_conflict"
	CodeIdempotencyInProgress ErrorCode = "idempotency_in_progress"
	CodeInvalidPrecondition   ErrorCode = "invalid_precondition"
	CodePreconditionFailed    ErrorCode = "precondition_failed"
	CodeUnavailable           ErrorCode = "record_unavailable"
	CodeInvalidRecordID       ErrorCode = "invalid_record_id"
	CodeRecordIDConflict      ErrorCode = "record_id_conflict"
)

// Error exposes a stable, non-sensitive failure code. CurrentETag is populated
// only for a failed precondition against an existing record.
type Error struct {
	Code        ErrorCode
	CurrentETag string
	canceled    bool
}

func (e *Error) Error() string {
	if e == nil {
		return "record operation failed"
	}
	return string(e.Code)
}
