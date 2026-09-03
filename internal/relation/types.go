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

// WriteMode controls whether an omitted relation is an empty create-time value
// or an intentionally untouched patch value.
type WriteMode uint8

const (
	CreateWrite WriteMode = iota + 1
	PatchWrite
)

// sourceState tells lockRows whether the source row already exists. A pending
// source is used during create after its ID is allocated but before the base
// insert; its own ID is accepted as a self-relation target. A stored source is
// locked FOR UPDATE before any mutation proceeds.
type sourceState uint8

const (
	sourcePending sourceState = iota + 1
	sourceStored
)

type ValidationDetail = validationdomain.Detail
type ValidationError = validationdomain.Error

var (
	// ErrInvalidBinding means catalog metadata could not be safely bound to
	// physical relation storage.
	ErrInvalidBinding = errors.New("invalid relation catalog binding")
	// ErrSourceNotFound is returned only for sourceStored. Target misses always
	// use relation_target_invalid so they do not become an existence oracle.
	ErrSourceNotFound = errors.New("relation source not found")
	// ErrInvalidStoredValue reports catalog/storage drift without including the
	// physical relation object or stored record value in its text.
	ErrInvalidStoredValue = errors.New("invalid stored relation value")
)

// relationValue is the normalized representation of one relation field. IDs preserve
// request order. JSON is canonical (lower-case UUIDs) and distinguishes an
// omitted value (Present=false) from a clear (Present=true, Clear=true).
type relationValue struct {
	FieldName string
	Present   bool
	Clear     bool
	IDs       []uuid.UUID
	JSON      json.RawMessage

	fieldID            uuid.UUID
	targetCollectionID uuid.UUID
	cardinality        schema.RelationCardinality
}

// writeInput is the lossless split between values handled by the scalar
// compiler and normalized relation values handled by this package. Scalar
// contains cloned JSON bytes; a present single relation is included there in
// canonical form, while multi relations stay in relations.
type writeInput struct {
	Scalar map[string]json.RawMessage

	sourceCollectionID uuid.UUID
	relations          []relationValue
}

func validation(field, code string) error {
	return &ValidationError{Details: []ValidationDetail{{Field: field, Code: code}}}
}
