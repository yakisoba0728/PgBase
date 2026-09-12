package schema

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func planViewRename(catalog Catalog, current Collection, input UpdateCollectionInput) (Plan, bool, error) {
	if input.Name == current.Name || input.View != nil || input.Fields != nil || input.Indexes != nil {
		return Plan{}, false, nil
	}
	statements, fields, err := renameCollectionStatements(current, input.Name, nil, nil)
	if err != nil {
		return Plan{}, true, err
	}
	view := &PlannedView{SourceCollectionID: current.View.SourceCollectionID, Source: current.View.Source, Projection: append([]string(nil), current.View.Projection...), Filter: cloneViewFilter(current.View.Filter), SQL: current.View.SQL, Dependencies: append([]uuid.UUID(nil), current.View.Dependencies...)}
	for _, field := range current.Fields {
		planned, err := plannedFieldFromCatalog(catalog, field)
		if err != nil {
			return Plan{}, true, err
		}
		fields = append(fields, planned)
	}
	definition := PlannedCollection{Name: input.Name, CompatibilityName: current.CompatibilityName, Type: current.Type, System: current.System, ValidationMode: current.ValidationMode, TableName: input.Name, Fields: fields, View: view}
	return Plan{Operation: updateCollectionOperation, BaseVersion: catalog.Version, Definition: definition, Statements: statements}, true, nil
}

func updateFieldsFromCatalog(catalog Catalog, current Collection) ([]FieldInput, error) {
	fields := make([]FieldInput, 0, len(current.Fields))
	for _, field := range current.Fields {
		system := field.System
		value := FieldInput{ID: &field.ID, Name: field.Name, Type: field.Type, Required: field.Required, Unique: field.Unique, Indexed: field.Indexed, Default: append(json.RawMessage(nil), field.Default...), Options: cloneFieldOptionsPointer(field.Options), Hidden: field.Hidden, Presentable: field.Presentable, Help: field.Help, System: &system}
		if field.Relation != nil {
			target, ok := collectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return nil, ErrConflict
			}
			value.Relation = &RelationInput{TargetCollection: target.Name, Cardinality: field.Relation.Cardinality, MinSelect: intPointer(field.Relation.MinSelect), MaxSelect: intPointer(field.Relation.MaxSelect), OnDelete: field.Relation.OnDelete}
		}
		fields = append(fields, value)
	}
	return fields, nil
}

func collectionForUpdate(catalog Catalog, input UpdateCollectionInput) (Collection, bool) {
	if input.CollectionID == nil {
		collection, ok := catalog.Collections[input.Name]
		return collection, ok
	}
	for _, collection := range catalog.Collections {
		if collection.ID == *input.CollectionID {
			return collection, true
		}
	}
	return Collection{}, false
}

func validateCollectionUpdateTarget(catalog Catalog, current Collection, input UpdateCollectionInput) error {
	if input.CollectionID == nil && current.Name != input.Name {
		return ErrConflict
	}
	if input.CollectionID != nil && *input.CollectionID != current.ID {
		return ErrConflict
	}
	if err := validateInputName("name", input.Name); err != nil {
		return err
	}
	if existing, exists := catalog.Collections[input.Name]; exists && existing.ID != current.ID {
		return validation("name", "duplicate_name")
	}
	return nil
}

func finalizeCollectionRename(current Collection, target string, fields []PlannedField, indexes []PlannedIndex, statements []string) ([]PlannedField, []string, error) {
	comparisonIndexes := current.Indexes
	if target != current.Name {
		comparisonIndexes = catalogIndexesForCollection(current.Indexes, target)
	}
	statements = append(statements, compoundIndexChangeStatements(current.Name, current.TableName, comparisonIndexes, indexes, fields)...)
	if target == current.Name {
		return fields, statements, nil
	}
	if len(statements) != 0 {
		return nil, nil, validation("name", "rename_requires_unchanged_definition")
	}
	renameStatements, fields, err := renameCollectionStatements(current, target, fields, indexes)
	if err != nil {
		return nil, nil, err
	}
	for index := range fields {
		if fields[index].Relation != nil && fields[index].Relation.TargetCollectionID != nil && *fields[index].Relation.TargetCollectionID == current.ID {
			fields[index].Relation.TargetCollection = target
		}
	}
	return fields, append(statements, renameStatements...), nil
}

func renameCollectionStatements(current Collection, target string, fields []PlannedField, indexes []PlannedIndex) ([]string, []PlannedField, error) {
	if current.Name == target {
		return nil, fields, nil
	}
	if current.System {
		return nil, nil, validation("name", "system_collection_immutable")
	}
	if current.IsView() {
		return []string{"ALTER VIEW " + pgx.Identifier{"app", current.TableName}.Sanitize() + " RENAME TO " + pgx.Identifier{target}.Sanitize()}, fields, nil
	}
	statements := []string{}
	recreate := []string{}
	for index := range fields {
		field := &fields[index]
		if field.Relation == nil {
			continue
		}
		if field.Relation.TargetCollectionID != nil && *field.Relation.TargetCollectionID == current.ID {
			field.Relation.TargetCollection = target
		}
		before := NewRelationPhysicalNames(current.TableName, field.ColumnName)
		after := NewRelationPhysicalNames(target, field.ColumnName)
		if field.Relation.Cardinality == RelationSingle {
			statements = append(statements,
				"ALTER TABLE "+pgx.Identifier{"app", current.TableName}.Sanitize()+" RENAME CONSTRAINT "+pgx.Identifier{before.SingleForeignKey}.Sanitize()+" TO "+pgx.Identifier{after.SingleForeignKey}.Sanitize(),
				"ALTER INDEX "+pgx.Identifier{"app", before.SingleReverseIndex}.Sanitize()+" RENAME TO "+pgx.Identifier{after.SingleReverseIndex}.Sanitize(),
			)
			continue
		}
		oldEdge := pgx.Identifier{"pgbase_relation", before.MultiTable}.Sanitize()
		newEdge := pgx.Identifier{"pgbase_relation", after.MultiTable}.Sanitize()
		statements = append(statements,
			"DROP TRIGGER "+pgx.Identifier{before.PositionTrigger}.Sanitize()+" ON "+oldEdge,
			"DROP TRIGGER "+pgx.Identifier{before.EdgeCardinalityTrigger}.Sanitize()+" ON "+oldEdge,
			"DROP TRIGGER "+pgx.Identifier{before.SourceCardinalityTrigger}.Sanitize()+" ON "+pgx.Identifier{"app", current.TableName}.Sanitize(),
			"DROP FUNCTION "+pgx.Identifier{"pgbase_relation", before.PositionFunction}.Sanitize()+"()",
			"DROP FUNCTION "+pgx.Identifier{"pgbase_relation", before.CardinalityFunction}.Sanitize()+"()",
			"ALTER TABLE "+oldEdge+" RENAME TO "+pgx.Identifier{after.MultiTable}.Sanitize(),
		)
		for _, pair := range [][2]string{
			{before.MultiSourceForeignKey, after.MultiSourceForeignKey},
			{before.MultiTargetForeignKey, after.MultiTargetForeignKey},
			{before.MultiPrimaryKey, after.MultiPrimaryKey},
			{before.MultiPositionUnique, after.MultiPositionUnique},
			{before.MultiPositionCheck, after.MultiPositionCheck},
		} {
			statements = append(statements, "ALTER TABLE "+newEdge+" RENAME CONSTRAINT "+pgx.Identifier{pair[0]}.Sanitize()+" TO "+pgx.Identifier{pair[1]}.Sanitize())
		}
		statements = append(statements, "ALTER INDEX "+pgx.Identifier{"pgbase_relation", before.MultiReverseIndex}.Sanitize()+" RENAME TO "+pgx.Identifier{after.MultiReverseIndex}.Sanitize())
		field.Relation.Storage.Name = after.MultiTable
		generated := createMultiRelationStatements(target, *field)
		if len(generated) != 7 {
			return nil, nil, fmt.Errorf("unexpected relation DDL shape")
		}
		recreate = append(recreate, generated[2:]...)
	}
	for _, field := range current.Fields {
		if field.Type == TypeRelation {
			continue
		}
		if field.Unique || field.Indexed {
			statements = append(statements, "ALTER INDEX "+pgx.Identifier{"app", indexName(current.Name, field.Name)}.Sanitize()+" RENAME TO "+pgx.Identifier{indexName(target, field.Name)}.Sanitize())
		}
	}
	statements = append(statements, "ALTER INDEX "+pgx.Identifier{"app", indexName(current.Name, "created_at:id")}.Sanitize()+" RENAME TO "+pgx.Identifier{indexName(target, "created_at:id")}.Sanitize())
	for _, index := range indexes {
		statements = append(statements, "ALTER INDEX "+pgx.Identifier{"app", compoundIndexName(current.Name, index.Name)}.Sanitize()+" RENAME TO "+pgx.Identifier{compoundIndexName(target, index.Name)}.Sanitize())
	}
	statements = append(statements, "ALTER TABLE "+pgx.Identifier{"app", current.TableName}.Sanitize()+" RENAME TO "+pgx.Identifier{target}.Sanitize())
	statements = append(statements, recreate...)
	return statements, fields, nil
}

func collectionIDResourceName(id uuid.UUID) string { return "id:" + id.String() }
