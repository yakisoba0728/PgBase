// Package query converts record JSON values and compiles deterministic SQL
// against immutable, validated schema catalog metadata.
package query

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Statement struct {
	SQL  string
	Args []any
}

type PageCursor struct {
	Version       int
	CollectionID  uuid.UUID
	Sort          []SortKey
	Values        []json.RawMessage
	AnchorID      uuid.UUID
	AnchorHash    []byte
	PrivateValues bool
	ExpiresAt     time.Time
}

type RowScanner interface {
	Scan(...any) error
}

type ValidationDetail struct {
	Field string
	Code  string
}

type ValidationError struct {
	Details []ValidationDetail
}

func (e *ValidationError) Error() string { return "record validation failed" }

type InvalidCursorError struct{}

func (e *InvalidCursorError) Error() string { return "invalid cursor" }

// RecordScanError identifies a database row scan failure without exposing the
// driver or server error through its text or the public error chain.
type RecordScanError struct {
	cause error
}

func (e *RecordScanError) Error() string { return "record scan failed" }

func (e *RecordScanError) diagnosticCause() error { return e.cause }

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}

func validationField(err error, field string) bool {
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		return false
	}
	for _, detail := range validationError.Details {
		if detail.Field == field {
			return true
		}
	}
	return false
}

func invalidCursor() error { return &InvalidCursorError{} }

func recordScanError(cause error) error { return &RecordScanError{cause: cause} }

func IsInvalidCursor(err error) bool {
	var target *InvalidCursorError
	return errors.As(err, &target)
}
