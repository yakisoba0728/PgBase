// Package schema defines collection metadata and deterministic schema plans.
package schema

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	validationdomain "github.com/yakisoba0728/PgBase/internal/validation"
)

type FieldType string

const (
	TypeBoolean     FieldType = "boolean"
	TypeInteger     FieldType = "integer"
	TypeBigInt      FieldType = "bigint"
	TypeNumeric     FieldType = "numeric"
	TypeText        FieldType = "text"
	TypeEditor      FieldType = "editor"
	TypeGeoPoint    FieldType = "geoPoint"
	TypeAutoDate    FieldType = "autodate"
	TypePassword    FieldType = "password"
	TypeTimestampTZ FieldType = "timestamptz"
	TypeUUID        FieldType = "uuid"
	TypeJSONB       FieldType = "jsonb"
	TypeRelation    FieldType = "relation"
	// TypeFile is metadata-only. File bytes and attachment metadata live in
	// pgbase_meta.record_files, so dynamic record tables have no file column.
	TypeFile FieldType = "file"
)

type FieldInput struct {
	ID          *uuid.UUID      `json:"id,omitempty"`
	Name        string          `json:"name"`
	Type        FieldType       `json:"type"`
	Required    bool            `json:"required"`
	Unique      bool            `json:"unique"`
	Indexed     bool            `json:"indexed"`
	Default     json.RawMessage `json:"default,omitempty"`
	Relation    *RelationInput  `json:"relation,omitempty"`
	Options     *FieldOptions   `json:"options,omitempty"`
	Hidden      bool            `json:"hidden,omitempty"`
	Presentable bool            `json:"presentable,omitempty"`
	Help        string          `json:"help,omitempty"`
	System      *bool           `json:"system,omitempty"`
}

// FieldOptions contains value constraints enforced on record writes. Required
// remains the SQL NULL contract; these constraints apply only to non-NULL values.
type FieldOptions struct {
	MinLength      *int     `json:"minLength,omitempty"`
	MaxLength      *int     `json:"maxLength,omitempty"`
	Pattern        string   `json:"pattern,omitempty"`
	Format         string   `json:"format,omitempty"`
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	DeniedDomains  []string `json:"deniedDomains,omitempty"`
	// AutogeneratePattern is a bounded generatable RE2 pattern used only when
	// a text field is omitted from a create input.
	AutogeneratePattern string   `json:"autogeneratePattern,omitempty"`
	AllowedValues       []string `json:"allowedValues,omitempty"`
	MinSelect           *int     `json:"minSelect,omitempty"`
	MaxSelect           *int     `json:"maxSelect,omitempty"`
	Min                 *string  `json:"min,omitempty"`
	Max                 *string  `json:"max,omitempty"`
	// File options are evaluated per attachment. MaxFileSize uses bytes;
	// MaxSelect defaults to one when omitted. MimeTypes accepts exact media
	// types and type wildcards such as image/*.
	MaxFileSize *int64   `json:"maxFileSize,omitempty"`
	MimeTypes   []string `json:"mimeTypes,omitempty"`
	Thumbs      []string `json:"thumbs,omitempty"`
	MaxSize     *int64   `json:"maxSize,omitempty"`
	ConvertURLs bool     `json:"convertURLs,omitempty"`
	OnCreate    bool     `json:"onCreate,omitempty"`
	OnUpdate    bool     `json:"onUpdate,omitempty"`
	// Protected defaults to true for file fields when omitted. Explicit false
	// opts a file field into public delivery policy.
	Protected *bool `json:"protected,omitempty"`
}

type CollectionType string

const (
	CollectionTypeBase CollectionType = "base"
	CollectionTypeAuth CollectionType = "auth"
	CollectionTypeView CollectionType = "view"
)

type ValidationMode string

const (
	ValidationModePostgres   ValidationMode = "postgres"
	ValidationModePocketBase ValidationMode = "pocketbase"
)

type IndexInput struct {
	Name   string          `json:"name"`
	Fields []string        `json:"fields,omitempty"`
	Unique bool            `json:"unique"`
	Keys   []IndexKeyInput `json:"keys,omitempty"`
	Where  string          `json:"where,omitempty"`
	RawSQL string          `json:"rawSql,omitempty"`
}

// IndexKeyInput is one btree key. Field and Expression are mutually exclusive.
// Expressions and partial predicates use PostgreSQL syntax but are accepted
// only after the planner has parsed and allowlisted their AST.
type IndexKeyInput struct {
	Field      string `json:"field,omitempty"`
	Expression string `json:"expression,omitempty"`
	Collation  string `json:"collation,omitempty"`
	Order      string `json:"order,omitempty"`
}

type CreateCollectionInput struct {
	Name              string          `json:"name"`
	CompatibilityName string          `json:"compatibilityName,omitempty"`
	Type              CollectionType  `json:"type,omitempty"`
	System            bool            `json:"system,omitempty"`
	ValidationMode    ValidationMode  `json:"validationMode,omitempty"`
	RecordID          *RecordIDPolicy `json:"recordId,omitempty"`
	Fields            []FieldInput    `json:"fields"`
	Indexes           []IndexInput    `json:"indexes,omitempty"`
	View              *ViewInput      `json:"view,omitempty"`
}

type ViewInput struct {
	Source     string      `json:"source"`
	Projection []string    `json:"projection"`
	Filter     *ViewFilter `json:"filter,omitempty"`
	SQL        string      `json:"sql,omitempty"`
	inspection *SQLViewInspection
}

type ViewFilter struct {
	Op    string          `json:"op"`
	Args  []ViewFilter    `json:"args,omitempty"`
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type UpdateCollectionInput struct {
	// CollectionID selects the stable collection identity for a rename. When it
	// is omitted Name keeps its historical role as both selector and target.
	CollectionID      *uuid.UUID      `json:"collectionId,omitempty"`
	Name              string          `json:"name"`
	CompatibilityName *string         `json:"compatibilityName,omitempty"`
	Type              *CollectionType `json:"type,omitempty"`
	System            *bool           `json:"system,omitempty"`
	ValidationMode    *ValidationMode `json:"validationMode,omitempty"`
	RecordID          *RecordIDPolicy `json:"recordId,omitempty"`
	Fields            []FieldInput    `json:"fields"`
	Indexes           *[]IndexInput   `json:"indexes,omitempty"`
	View              *ViewInput      `json:"view,omitempty"`
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
		view.inspection = cloneSQLViewInspection(view.inspection)
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
	if input.View != nil {
		view := *input.View
		view.Projection = append([]string(nil), view.Projection...)
		view.Filter = cloneViewFilter(view.Filter)
		view.inspection = cloneSQLViewInspection(view.inspection)
		input.View = &view
	}
	return Change{kind: changeUpdate, update: input}
}

func cloneIndexInputs(indexes []IndexInput) []IndexInput {
	cloned := append([]IndexInput(nil), indexes...)
	for index := range cloned {
		cloned[index].Fields = append([]string(nil), indexes[index].Fields...)
		cloned[index].Keys = append([]IndexKeyInput(nil), indexes[index].Keys...)
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

func cloneFieldOptions(source FieldOptions) FieldOptions {
	result := source
	result.MinLength = clonePointer(source.MinLength)
	result.MaxLength = clonePointer(source.MaxLength)
	result.MinSelect = clonePointer(source.MinSelect)
	result.MaxSelect = clonePointer(source.MaxSelect)
	result.Min = clonePointer(source.Min)
	result.Max = clonePointer(source.Max)
	result.MaxFileSize = clonePointer(source.MaxFileSize)
	result.MaxSize = clonePointer(source.MaxSize)
	result.Protected = clonePointer(source.Protected)
	result.AllowedDomains = append([]string(nil), source.AllowedDomains...)
	result.DeniedDomains = append([]string(nil), source.DeniedDomains...)
	result.AllowedValues = append([]string(nil), source.AllowedValues...)
	result.MimeTypes = append([]string(nil), source.MimeTypes...)
	result.Thumbs = append([]string(nil), source.Thumbs...)
	return result
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
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	ColumnName  string          `json:"columnName"`
	Type        FieldType       `json:"type"`
	Required    bool            `json:"required"`
	Unique      bool            `json:"unique"`
	Indexed     bool            `json:"indexed"`
	Default     json.RawMessage `json:"default,omitempty"`
	Relation    *RelationSpec   `json:"relation,omitempty"`
	Options     FieldOptions    `json:"options,omitzero"`
	Hidden      bool            `json:"hidden,omitempty"`
	Presentable bool            `json:"presentable,omitempty"`
	Help        string          `json:"help,omitempty"`
	System      bool            `json:"system,omitempty"`
	Ordinal     int             `json:"ordinal"`
}

type Collection struct {
	ID                uuid.UUID      `json:"id"`
	Name              string         `json:"name"`
	CompatibilityName string         `json:"compatibilityName,omitempty"`
	Type              CollectionType `json:"type"`
	System            bool           `json:"system,omitempty"`
	ValidationMode    ValidationMode `json:"validationMode"`
	RecordID          RecordIDPolicy `json:"recordId"`
	TableName         string         `json:"tableName"`
	Fields            []Field        `json:"fields"`
	Indexes           []Index        `json:"indexes,omitempty"`
	View              *ViewSpec      `json:"view,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
}

type ViewSpec struct {
	SourceCollectionID uuid.UUID   `json:"sourceCollectionId"`
	Source             string      `json:"source"`
	Projection         []string    `json:"projection"`
	Filter             *ViewFilter `json:"filter,omitempty"`
	SQL                string      `json:"sql,omitempty"`
	Dependencies       []uuid.UUID `json:"dependencies,omitempty"`
}

func (c Collection) IsView() bool    { return c.View != nil }
func (c Collection) IsSQLView() bool { return c.View != nil && c.View.SQL != "" }
func (c Collection) AccessCollectionID() uuid.UUID {
	if c.View != nil && !c.IsSQLView() {
		return c.View.SourceCollectionID
	}
	return c.ID
}

type Index struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Fields    []string        `json:"fields"`
	IndexName string          `json:"indexName"`
	Unique    bool            `json:"unique"`
	Keys      []IndexKeyInput `json:"keys,omitempty"`
	Where     string          `json:"where,omitempty"`
	RawSQL    string          `json:"rawSql,omitempty"`
}

type PlannedField struct {
	ID          uuid.UUID `json:"id,omitzero"`
	Name        string
	ColumnName  string
	Type        FieldType
	Required    bool
	Unique      bool
	Indexed     bool
	Default     json.RawMessage
	Relation    *PlannedRelation `json:"relation,omitempty"`
	Options     FieldOptions     `json:"options,omitzero"`
	Hidden      bool             `json:"hidden,omitempty"`
	Presentable bool             `json:"presentable,omitempty"`
	Help        string           `json:"help,omitempty"`
	System      bool             `json:"system,omitempty"`
	Ordinal     int
}

type PlannedCollection struct {
	Name              string
	CompatibilityName string
	Type              CollectionType
	System            bool
	ValidationMode    ValidationMode
	RecordID          RecordIDPolicy
	TableName         string
	Fields            []PlannedField
	Indexes           []PlannedIndex
	View              *PlannedView `json:"view,omitempty"`
}
type PlannedView struct {
	SourceCollectionID uuid.UUID   `json:"sourceCollectionId"`
	Source             string      `json:"source"`
	Projection         []string    `json:"projection"`
	Filter             *ViewFilter `json:"filter,omitempty"`
	SQL                string      `json:"sql,omitempty"`
	Dependencies       []uuid.UUID `json:"dependencies,omitempty"`
}

type PlannedIndex struct {
	Name   string          `json:"name"`
	Fields []string        `json:"fields"`
	Unique bool            `json:"unique"`
	Keys   []IndexKeyInput `json:"keys,omitempty"`
	Where  string          `json:"where,omitempty"`
	RawSQL string          `json:"rawSql,omitempty"`
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
