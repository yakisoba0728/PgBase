// Package recordops owns the principal-aware transaction boundary for record
// reads and mutations. Callers provide authenticated identity and bounded HTTP
// metadata; the package keeps record, revision, audit, outbox, and idempotency
// state atomic in PostgreSQL.
package recordops

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/query"
)

type PrincipalKind = identity.Kind

const PrincipalAdministrator PrincipalKind = identity.Administrator

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
	Replayed      bool
	Compatibility Compatibility
}

type ListRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
	Cursor     string
	Limit      int
	Filter     string
	Sort       string
	Expand     string
}

type GetRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
	ID         uuid.UUID
	Expand     string
}

type CreateRequest struct {
	Principal      Principal
	RequestID      string
	Collection     string
	Input          map[string]json.RawMessage
	IdempotencyKey string
}

type UpdateRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
	ID         uuid.UUID
	Input      map[string]json.RawMessage
	IfMatch    EntityTagSet
}

type DeleteRequest struct {
	Principal  Principal
	RequestID  string
	Collection string
	ID         uuid.UUID
	IfMatch    EntityTagSet
}

type RecordPage = query.RecordPage

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
