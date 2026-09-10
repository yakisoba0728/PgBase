// Package query converts record JSON values and compiles deterministic SQL
// against immutable, validated schema catalog metadata.
package query

import (
	"encoding/json"
	"errors"

	validationdomain "github.com/yakisoba0728/PgBase/internal/validation"
)

type Statement struct {
	SQL  string
	Args []any
}

type pageBoundary struct {
	Values     []json.RawMessage
	membership *int64
	// Only a v5 traversal's first page may carry no keyset values, its high-water
	// being the whole boundary. Every other boundary must reach orderSpec.keyset so
	// a short value list fails as an invalid cursor, not as a restart at page 1.
	membershipOnly bool
}

func (boundary *pageBoundary) membershipBoundary() *int64 {
	if boundary == nil {
		return nil
	}
	return boundary.membership
}

type RowScanner interface {
	Scan(...any) error
}

type ValidationDetail = validationdomain.Detail
type ValidationError = validationdomain.Error

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
