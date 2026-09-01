// Package relation owns the record-value side of schema relations.
//
// A Bindings value is created from one validated catalog snapshot and is then
// used to decode request values, acquire record-operation locks, write ordered
// multi-value edges, and hydrate those edges after a base-record query. This
// keeps physical relation identifiers and lock ordering out of callers.
package relation

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/schema"
	validationdomain "github.com/webisnoteasy/PgBase/internal/validation"
)

// InputMode controls whether an omitted relation is an empty create-time value
// or an intentionally untouched patch value.
type InputMode uint8

const (
	CreateInput InputMode = iota + 1
	PatchInput
)

// SourceState tells LockRows whether the source row already exists. A pending
// source is used during create after its ID is allocated but before the base
// insert; its own ID is accepted as a self-relation target. A stored source is
// locked FOR UPDATE before any mutation proceeds.
type SourceState uint8

const (
	SourcePending SourceState = iota + 1
	SourceStored
)

type ValidationDetail = validationdomain.Detail
type ValidationError = validationdomain.Error

var (
	// ErrInvalidBinding means catalog metadata could not be safely bound to
	// physical relation storage.
	ErrInvalidBinding = errors.New("invalid relation catalog binding")
	// ErrSourceNotFound is returned only for SourceStored. Target misses always
	// use relation_target_invalid so they do not become an existence oracle.
	ErrSourceNotFound = errors.New("relation source not found")
	// ErrInvalidStoredValue reports catalog/storage drift without including the
	// physical relation object or stored record value in its text.
	ErrInvalidStoredValue = errors.New("invalid stored relation value")
)

// Value is the normalized representation of one relation field. IDs preserve
// request order. JSON is canonical (lower-case UUIDs) and distinguishes an
// omitted value (Present=false) from a clear (Present=true, Clear=true).
type Value struct {
	FieldName string
	Present   bool
	Clear     bool
	IDs       []uuid.UUID
	JSON      json.RawMessage

	fieldID            uuid.UUID
	targetCollectionID uuid.UUID
	cardinality        schema.RelationCardinality
}

// Input is the lossless split between values handled by the scalar compiler
// and normalized relation values handled by this package. Scalar contains
// cloned JSON bytes; a present single relation is included there in canonical
// form, while multi relations are kept exclusively in Relations().
type Input struct {
	Scalar map[string]json.RawMessage

	sourceCollectionID uuid.UUID
	relations          []Value
}

// Relations returns a deep copy in stable field-ordinal order.
func (i Input) Relations() []Value {
	values := make([]Value, len(i.relations))
	for index, value := range i.relations {
		values[index] = cloneValue(value)
	}
	return values
}

func cloneValue(value Value) Value {
	value.IDs = append([]uuid.UUID(nil), value.IDs...)
	value.JSON = append(json.RawMessage(nil), value.JSON...)
	return value
}

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}
