// Package schema defines collection metadata and deterministic schema plans.
package schema

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	validationdomain "github.com/webisnoteasy/PgBase/internal/validation"
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
	TypeRelation    FieldType = "relation"
)

type FieldInput struct {
	Name     string          `json:"name"`
	Type     FieldType       `json:"type"`
	Required bool            `json:"required"`
	Unique   bool            `json:"unique"`
	Indexed  bool            `json:"indexed"`
	Default  json.RawMessage `json:"default,omitempty"`
	Relation *RelationInput  `json:"relation,omitempty"`
}

type CreateCollectionInput struct {
	Name   string       `json:"name"`
	Fields []FieldInput `json:"fields"`
}

type UpdateCollectionInput struct {
	Name   string       `json:"name"`
	Fields []FieldInput `json:"fields"`
}

type changeKind uint8

const (
	changeCreate changeKind = iota + 1
	changeUpdate
	changeDelete
)

// Change is an immutable schema intent. Values are created only through the
// operation-specific constructors so callers cannot combine incompatible
// create, update, and delete members.
type Change struct {
	kind       changeKind
	create     CreateCollectionInput
	update     UpdateCollectionInput
	deleteName string
}

func NewCreateChange(input CreateCollectionInput) Change {
	input.Fields = cloneFieldInputs(input.Fields)
	return Change{kind: changeCreate, create: input}
}

func NewUpdateChange(input UpdateCollectionInput) Change {
	input.Fields = cloneFieldInputs(input.Fields)
	return Change{kind: changeUpdate, update: input}
}

func NewDeleteChange(name string) Change {
	return Change{kind: changeDelete, deleteName: name}
}

func cloneFieldInputs(fields []FieldInput) []FieldInput {
	if fields == nil {
		return nil
	}
	cloned := make([]FieldInput, len(fields))
	for index, field := range fields {
		cloned[index] = cloneFieldInput(field)
	}
	return cloned
}

// ApplyIntent binds one immutable change to the plan version/checksum approved
// by the caller and the administrator responsible for the mutation.
type ApplyIntent struct {
	mode               operationMode
	change             Change
	checksum           string
	baseVersion        int64
	actor              uuid.UUID
	operationID        uuid.UUID
	resourceKeyHash    [32]byte
	idempotencyKeyHash [32]byte
	requestHash        [32]byte
	planChecksum       [32]byte
}

func LegacyApply(change Change, expectedPlan Plan, actor uuid.UUID) ApplyIntent {
	return ApplyIntent{
		mode:        operationModeLegacy,
		change:      change,
		checksum:    expectedPlan.Checksum,
		baseVersion: expectedPlan.BaseVersion,
		actor:       actor,
	}
}

type ApplyResult struct {
	Collection Collection
	Deleted    bool
	Receipt    OperationReceipt
	Replayed   bool
}

type Field struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	ColumnName string          `json:"columnName"`
	Type       FieldType       `json:"type"`
	Required   bool            `json:"required"`
	Unique     bool            `json:"unique"`
	Indexed    bool            `json:"indexed"`
	Default    json.RawMessage `json:"default,omitempty"`
	Relation   *RelationSpec   `json:"relation,omitempty"`
	Ordinal    int             `json:"ordinal"`
}

type Collection struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	TableName string    `json:"tableName"`
	Fields    []Field   `json:"fields"`
	CreatedAt time.Time `json:"createdAt"`
}

type PlannedField struct {
	Name     string
	Type     FieldType
	Required bool
	Unique   bool
	Indexed  bool
	Default  json.RawMessage
	Relation *PlannedRelation `json:"relation,omitempty"`
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

type ValidationDetail = validationdomain.Detail
type ValidationError = validationdomain.Error
