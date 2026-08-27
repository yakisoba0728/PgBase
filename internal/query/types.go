// Package query converts record JSON values and compiles deterministic SQL
// against immutable, validated schema catalog metadata.
package query

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Statement struct {
	SQL  string
	Args []any
}

type PageCursor struct {
	Version   int
	CreatedAt time.Time
	ID        uuid.UUID
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

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}

func invalidCursor() error { return &InvalidCursorError{} }

func IsInvalidCursor(err error) bool {
	var target *InvalidCursorError
	return errors.As(err, &target)
}
