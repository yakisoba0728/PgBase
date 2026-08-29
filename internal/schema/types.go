// Package schema defines collection metadata and deterministic schema plans.
package schema

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type FieldType string

const (
	TypeBoolean     FieldType = "boolean"
	TypeInteger     FieldType = "integer"
	TypeBigInt      FieldType = "bigint"
	TypeNumeric     FieldType = "numeric"
	TypeText        FieldType = "text"
	TypeTimestampTZ FieldType = "timestamptz"
	TypeUUID        FieldType = "uuid"
	TypeJSONB       FieldType = "jsonb"
)

type FieldInput struct {
	Name     string          `json:"name"`
	Type     FieldType       `json:"type"`
	Required bool            `json:"required"`
	Unique   bool            `json:"unique"`
	Indexed  bool            `json:"indexed"`
	Default  json.RawMessage `json:"default,omitempty"`
}

type CreateCollectionInput struct {
	Name   string       `json:"name"`
	Fields []FieldInput `json:"fields"`
}

type UpdateCollectionInput struct {
	Name   string       `json:"name"`
	Fields []FieldInput `json:"fields"`
}

type Field struct {
	ID         uuid.UUID
	Name       string
	ColumnName string
	Type       FieldType
	Required   bool
	Unique     bool
	Indexed    bool
	Default    json.RawMessage
	Ordinal    int
}

type Collection struct {
	ID        uuid.UUID
	Name      string
	TableName string
	Fields    []Field
	CreatedAt time.Time
}

type PlannedField struct {
	Name     string
	Type     FieldType
	Required bool
	Unique   bool
	Indexed  bool
	Default  json.RawMessage
	Ordinal  int
}

type PlannedCollection struct {
	Name      string
	TableName string
	Fields    []PlannedField
}

type Plan struct {
	Operation   string
	BaseVersion int64
	Checksum    string
	Definition  PlannedCollection
	Statements  []string
}

type Planner struct{}

type PlanBuilder interface {
	PlanCreate(baseVersion int64, input CreateCollectionInput) (Plan, error)
	PlanUpdate(baseVersion int64, current Collection, input UpdateCollectionInput) (Plan, error)
	PlanDelete(baseVersion int64, current Collection) (Plan, error)
}

// ValidationDetail identifies a rejected input member without exposing it in an error string.
type ValidationDetail struct {
	Field string
	Code  string
}

// ValidationError reports stable, caller-addressable validation details.
type ValidationError struct {
	Details []ValidationDetail
}

func (e *ValidationError) Error() string { return "schema validation failed" }
