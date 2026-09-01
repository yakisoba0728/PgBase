// Package recordops owns the principal-aware transaction boundary for record
// reads and mutations. Callers provide authenticated identity and bounded HTTP
// metadata; the package keeps record, revision, audit, outbox, and idempotency
// state atomic in PostgreSQL.
package recordops

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/query"
)

// maximumRecordBytes bounds one record's JSON encoding at half the list page
// budget package query enforces on reads (maximumRecordPageBytes, 4 MiB, kept
// unexported there). The read path admits the first item of a page whatever
// its size and then fails the entire page with response_too_large when that
// single item does not fit, so one record above the page budget makes itself
// and every record after it in sort order permanently unreachable through
// list. Halving the budget leaves room for the page envelope and cursor and
// keeps any two bounded records splittable across two pages.
//
// The per-field bounds in package query cannot express this: decodeInput sees
// only the fields a request supplied, so a PATCH that grows a third text field
// has no way to know the size of the record it produces.
const maximumRecordBytes = 2 << 20

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

type Record struct {
	Data    map[string]any `json:"data"`
	ETag    string         `json:"-"`
	Version int64          `json:"-"`
}

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
)

// Error exposes a stable, non-sensitive failure code. CurrentETag is populated
// only for a failed precondition against an existing record.
type Error struct {
	Code        ErrorCode
	CurrentETag string
}

func (e *Error) Error() string {
	if e == nil {
		return "record operation failed"
	}
	return string(e.Code)
}

func IsCode(err error, code ErrorCode) bool {
	var operationError *Error
	return errors.As(err, &operationError) && operationError.Code == code
}
