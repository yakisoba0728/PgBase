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
	// TypeFile is metadata-only. File bytes and attachment metadata live in
	// pgbase_meta.record_files, so dynamic record tables have no file column.
	TypeFile FieldType = "file"
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

type IndexInput struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
	Unique bool     `json:"unique"`
}

type CreateCollectionInput struct {
	Name    string       `json:"name"`
	Fields  []FieldInput `json:"fields"`
	Indexes []IndexInput `json:"indexes,omitempty"`
	View    *ViewInput   `json:"view,omitempty"`
}

type ViewInput struct {
	Source     string      `json:"source"`
	Projection []string    `json:"projection"`
	Filter     *ViewFilter `json:"filter,omitempty"`
}

type ViewFilter struct {
	Op    string          `json:"op"`
	Args  []ViewFilter    `json:"args,omitempty"`
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type UpdateCollectionInput struct {
	Name    string        `json:"name"`
	Fields  []FieldInput  `json:"fields"`
	Indexes *[]IndexInput `json:"indexes,omitempty"`
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
	input.Indexes = cloneIndexInputs(input.Indexes)
	if input.View != nil {
		view := *input.View
		view.Projection = append([]string(nil), view.Projection...)
		view.Filter = cloneViewFilter(view.Filter)
		input.View = &view
	}
	return Change{kind: changeCreate, create: input}
}

func cloneViewFilter(filter *ViewFilter) *ViewFilter {
	if filter == nil {
		return nil
	}
	cloned := *filter
	cloned.Value = append(json.RawMessage(nil), filter.Value...)
	cloned.Args = append([]ViewFilter(nil), filter.Args...)
	for i := range cloned.Args {
		child := cloneViewFilter(&filter.Args[i])
		cloned.Args[i] = *child
	}
	return &cloned
}

func NewUpdateChange(input UpdateCollectionInput) Change {
	input.Fields = cloneFieldInputs(input.Fields)
	if input.Indexes != nil {
		cloned := cloneIndexInputs(*input.Indexes)
		input.Indexes = &cloned
	}
	return Change{kind: changeUpdate, update: input}
}

func cloneIndexInputs(indexes []IndexInput) []IndexInput {
	cloned := append([]IndexInput(nil), indexes...)
	for index := range cloned {
		cloned[index].Fields = append([]string(nil), indexes[index].Fields...)
	}
	return cloned
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
	Indexes   []Index   `json:"indexes,omitempty"`
	View      *ViewSpec `json:"view,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type ViewSpec struct {
	SourceCollectionID uuid.UUID   `json:"sourceCollectionId"`
	Source             string      `json:"source"`
	Projection         []string    `json:"projection"`
	Filter             *ViewFilter `json:"filter,omitempty"`
}

func (c Collection) IsView() bool { return c.View != nil }
func (c Collection) AccessCollectionID() uuid.UUID {
	if c.View != nil {
		return c.View.SourceCollectionID
	}
	return c.ID
}

type Index struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Fields    []string  `json:"fields"`
	IndexName string    `json:"indexName"`
	Unique    bool      `json:"unique"`
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
	Indexes   []PlannedIndex
	View      *PlannedView `json:"view,omitempty"`
}
type PlannedView struct {
	SourceCollectionID uuid.UUID   `json:"sourceCollectionId"`
	Source             string      `json:"source"`
	Projection         []string    `json:"projection"`
	Filter             *ViewFilter `json:"filter,omitempty"`
}

type PlannedIndex struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
	Unique bool     `json:"unique"`
}

type Plan struct {
	Operation   string
	BaseVersion int64
	Checksum    string
	Definition  PlannedCollection
	Statements  []string
}

type ValidationDetail = validationdomain.Detail
type ValidationError = validationdomain.Error
