package relation

import (
	"regexp"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

var catalogIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type collectionBinding struct {
	id       uuid.UUID
	tableSQL string
}

type fieldBinding struct {
	field      schema.Field
	spec       schema.RelationSpec
	storageSQL string
}

// Bindings is an immutable, validated view of one source collection's
// relation fields. Its zero value is invalid.
type Bindings struct {
	source       collectionBinding
	sourceFields []schema.Field
	fields       []fieldBinding
	fieldsByName map[string]int
	collections  map[uuid.UUID]collectionBinding
}

// Bind validates relation metadata and resolves every target against the same
// pinned catalog snapshot. Physical identifiers are rendered exactly once
// with pgx.Identifier after this function accepts the binding.
func Bind(catalog schema.Catalog, source schema.Collection) (*Bindings, error) {
	canonical, ok := catalog.Collections[source.Name]
	if !ok || !sameCollectionIdentity(canonical, source) {
		return nil, ErrInvalidBinding
	}

	collections := make(map[uuid.UUID]collectionBinding, len(catalog.Collections))
	tableNames := make(map[string]struct{}, len(catalog.Collections))
	for name, collection := range catalog.Collections {
		if name != collection.Name || collection.ID == uuid.Nil ||
			!catalogIdentifier.MatchString(collection.TableName) {
			return nil, ErrInvalidBinding
		}
		if _, duplicate := collections[collection.ID]; duplicate {
			return nil, ErrInvalidBinding
		}
		if _, duplicate := tableNames[collection.TableName]; duplicate {
			return nil, ErrInvalidBinding
		}
		tableNames[collection.TableName] = struct{}{}
		collections[collection.ID] = collectionBinding{
			id:       collection.ID,
			tableSQL: pgx.Identifier{"app", collection.TableName}.Sanitize(),
		}
	}

	bound := &Bindings{
		source:       collections[canonical.ID],
		sourceFields: make([]schema.Field, len(canonical.Fields)),
		fieldsByName: make(map[string]int),
		collections:  collections,
	}
	for index, field := range canonical.Fields {
		bound.sourceFields[index] = cloneField(field)
	}
	sort.Slice(bound.sourceFields, func(left, right int) bool {
		if bound.sourceFields[left].Ordinal != bound.sourceFields[right].Ordinal {
			return bound.sourceFields[left].Ordinal < bound.sourceFields[right].Ordinal
		}
		return bound.sourceFields[left].Name < bound.sourceFields[right].Name
	})
	seenFieldIDs := make(map[uuid.UUID]struct{}, len(canonical.Fields))
	seenFieldNames := make(map[string]struct{}, len(canonical.Fields))
	for ordinal, field := range canonical.Fields {
		if field.ID == uuid.Nil || !catalogIdentifier.MatchString(field.Name) ||
			!catalogIdentifier.MatchString(field.ColumnName) || field.Name != field.ColumnName ||
			field.Ordinal != ordinal {
			return nil, ErrInvalidBinding
		}
		if _, duplicate := seenFieldIDs[field.ID]; duplicate {
			return nil, ErrInvalidBinding
		}
		if _, duplicate := seenFieldNames[field.Name]; duplicate {
			return nil, ErrInvalidBinding
		}
		seenFieldIDs[field.ID] = struct{}{}
		seenFieldNames[field.Name] = struct{}{}

		if field.Type != schema.TypeRelation {
			if field.Relation != nil {
				return nil, ErrInvalidBinding
			}
			continue
		}
		if field.Relation == nil || schema.ValidateRelationSpec("field", *field.Relation) != nil {
			return nil, ErrInvalidBinding
		}
		spec := *field.Relation
		if field.Required != (spec.MinSelect > 0) || !field.Indexed || len(field.Default) != 0 ||
			(spec.Cardinality == schema.RelationMultiple && field.Unique) {
			return nil, ErrInvalidBinding
		}
		_, ok := collections[spec.TargetCollectionID]
		if !ok {
			return nil, ErrInvalidBinding
		}
		switch spec.Storage.Kind {
		case schema.RelationStorageInlineFKV1:
			if spec.Cardinality != schema.RelationSingle || spec.Storage.Name != field.ColumnName {
				return nil, ErrInvalidBinding
			}
		case schema.RelationStorageEdgeTableV1:
			expected := schema.NewRelationPhysicalNames(canonical.TableName, field.Name).MultiTable
			if spec.Cardinality != schema.RelationMultiple || spec.Storage.Name != expected {
				return nil, ErrInvalidBinding
			}
		default:
			return nil, ErrInvalidBinding
		}

		storageSQL := pgx.Identifier{spec.Storage.Name}.Sanitize()
		if spec.Storage.Kind == schema.RelationStorageEdgeTableV1 {
			storageSQL = pgx.Identifier{"pgbase_relation", spec.Storage.Name}.Sanitize()
		}
		bound.fields = append(bound.fields, fieldBinding{
			field: cloneField(field), spec: spec, storageSQL: storageSQL,
		})
	}

	sort.Slice(bound.fields, func(i, j int) bool {
		if bound.fields[i].field.Ordinal != bound.fields[j].field.Ordinal {
			return bound.fields[i].field.Ordinal < bound.fields[j].field.Ordinal
		}
		return bound.fields[i].field.Name < bound.fields[j].field.Name
	})
	for index := range bound.fields {
		bound.fieldsByName[bound.fields[index].field.Name] = index
	}
	return bound, nil
}

// HasMultiple reports whether Hydrate has any work for this collection.
func (b *Bindings) HasMultiple() bool {
	if b == nil {
		return false
	}
	for _, field := range b.fields {
		if field.spec.Cardinality == schema.RelationMultiple {
			return true
		}
	}
	return false
}

func sameCollectionIdentity(left, right schema.Collection) bool {
	return left.ID == right.ID && left.Name == right.Name && left.TableName == right.TableName
}

func cloneField(field schema.Field) schema.Field {
	field.Default = append([]byte(nil), field.Default...)
	if field.Relation != nil {
		spec := *field.Relation
		field.Relation = &spec
	}
	return field
}

func (b *Bindings) accepts(input Input) bool {
	if b == nil || input.sourceCollectionID != b.source.id || len(input.relations) != len(b.fields) {
		return false
	}
	for index, value := range input.relations {
		field := b.fields[index]
		if value.fieldID != field.field.ID || value.FieldName != field.field.Name ||
			value.targetCollectionID != field.spec.TargetCollectionID || value.cardinality != field.spec.Cardinality {
			return false
		}
	}
	return true
}
