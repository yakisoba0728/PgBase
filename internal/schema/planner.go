package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	createCollectionOperation = "collection_create"
	updateCollectionOperation = "collection_update"
	deleteCollectionOperation = "collection_delete"
	postgresIdentifierLimit   = 63
	indexHashLength           = 16
	maximumCollectionFields   = 1597
)

func CompilePlan(catalog Catalog, change Change) (Plan, error) {
	switch change.kind {
	case changeCreate:
		return planCreate(catalog, change.create)
	case changeUpdate:
		current, ok := catalog.Collections[change.update.Name]
		if !ok {
			return Plan{}, ErrCollectionNotFound
		}
		return planUpdate(catalog, current, change.update)
	case changeDelete:
		current, ok := catalog.Collections[change.deleteName]
		if !ok {
			return Plan{}, ErrCollectionNotFound
		}
		return planDelete(catalog, current)
	default:
		return Plan{}, ErrConflict
	}
}

func planCreate(catalog Catalog, input CreateCollectionInput) (Plan, error) {
	if err := validateInputName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	fields := make([]PlannedField, 0, len(input.Fields))
	seenNames := make(map[string]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if err := validateInputName(namePath, field.Name); err != nil {
			return Plan{}, err
		}
		if _, duplicate := seenNames[field.Name]; duplicate {
			return Plan{}, validation(namePath, "duplicate_name")
		}
		seenNames[field.Name] = struct{}{}
		planned, err := planInputField(catalog, input.Name, uuid.Nil, field, ordinal, true)
		if err != nil {
			return Plan{}, err
		}
		fields = append(fields, planned)
	}

	definition := PlannedCollection{Name: input.Name, TableName: input.Name, Fields: fields}
	statements := makeCreateStatements(definition)
	plan := Plan{
		Operation:   createCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  definition,
		Statements:  statements,
	}
	checksum, err := planChecksum(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Checksum = checksum
	return plan, nil
}

// Field renames are not supported: a name that disappears is a drop and a name
// that appears is an add.
func planUpdate(catalog Catalog, current Collection, input UpdateCollectionInput) (Plan, error) {
	if err := validateInputName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if input.Name != current.Name || current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	currentFields := make(map[string]PlannedField, len(current.Fields))
	for _, field := range current.Fields {
		planned, err := plannedFieldFromCatalog(catalog, field)
		if err != nil {
			return Plan{}, err
		}
		// Defaults are stored in a jsonb column whose text rendering differs
		// from the Go-canonical form; re-normalize so an unchanged object or
		// "now" default does not re-emit SET DEFAULT on every update.
		if len(planned.Default) != 0 {
			canonical, _, err := normalizeStoredDefault(planned.Type, planned.Default)
			if err != nil {
				return Plan{}, ErrConflict
			}
			planned.Default = canonical
		}
		currentFields[planned.Name] = planned
	}

	desired := make([]PlannedField, 0, len(input.Fields))
	desiredNames := make(map[string]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if err := validateInputName(namePath, field.Name); err != nil {
			return Plan{}, err
		}
		if _, duplicate := desiredNames[field.Name]; duplicate {
			return Plan{}, validation(namePath, "duplicate_name")
		}
		desiredNames[field.Name] = struct{}{}

		carriedDefault := json.RawMessage(nil)
		if previous, exists := currentFields[field.Name]; exists {
			carriedDefault = unchangedStoredDefault(previous, field)
		}
		if len(carriedDefault) != 0 {
			field.Default = nil
		}
		planned, err := planInputField(catalog, current.Name, current.ID, field, ordinal, false)
		if err != nil {
			return Plan{}, err
		}
		if len(carriedDefault) != 0 {
			planned.Default = append(json.RawMessage(nil), carriedDefault...)
		}
		desired = append(desired, planned)
	}

	table := pgx.Identifier{"app", current.TableName}.Sanitize()
	statements := make([]string, 0, len(desired))
	for _, field := range current.Fields {
		if _, kept := desiredNames[field.Name]; kept {
			continue
		}
		if field.Type == TypeRelation && field.Relation != nil {
			statements = append(statements, dropRelationFieldStatements(current, field)...)
			continue
		}
		statements = append(statements, "ALTER TABLE "+table+" DROP COLUMN "+pgx.Identifier{field.Name}.Sanitize())
	}
	for ordinal, field := range desired {
		previous, exists := currentFields[field.Name]
		if !exists {
			if field.Required && len(field.Default) == 0 {
				// A required relation is valid when the source collection is
				// empty. The service checks row existence both while planning
				// and again under an apply-time table lock. Scalar fields retain
				// the unconditional default requirement.
				if field.Type != TypeRelation {
					return Plan{}, validation(fmt.Sprintf("fields.%d.required", ordinal), "missing_default")
				}
			}
			if field.Type == TypeRelation {
				statements = append(statements, addRelationFieldStatements(current.TableName, field)...)
				continue
			}
			statements = append(statements, addColumnStatement(table, field))
			// A brand-new field has no prior index state, so its flags must
			// produce the same CREATE (UNIQUE) INDEX that a create plan emits.
			statements = append(statements, indexChangeStatements(current.Name, table, PlannedField{}, field)...)
			continue
		}
		if previous.Type == TypeRelation || field.Type == TypeRelation {
			relationChanges, err := alterRelationFieldStatements(current.TableName, ordinal, previous, field)
			if err != nil {
				return Plan{}, err
			}
			statements = append(statements, relationChanges...)
			continue
		}
		alter, err := alterColumnStatements(table, ordinal, previous, field)
		if err != nil {
			return Plan{}, err
		}
		statements = append(statements, alter...)
		statements = append(statements, indexChangeStatements(current.Name, table, previous, field)...)
	}

	definition := PlannedCollection{Name: input.Name, TableName: current.TableName, Fields: desired}
	plan := Plan{
		Operation:   updateCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  definition,
		Statements:  statements,
	}
	checksum, err := planChecksum(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Checksum = checksum
	return plan, nil
}

func unchangedStoredDefault(previous PlannedField, desired FieldInput) json.RawMessage {
	if previous.Type != desired.Type || len(previous.Default) == 0 || len(desired.Default) == 0 {
		return nil
	}
	canonical, _, err := normalizeStoredDefault(desired.Type, desired.Default)
	if err != nil || !bytes.Equal(previous.Default, canonical) {
		return nil
	}
	return previous.Default
}

// Dependent indexes are dropped with the table.
func planDelete(catalog Catalog, current Collection) (Plan, error) {
	if current.Name == "" || current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	if collectionHasIncomingRelation(catalog, current.ID) {
		return Plan{}, validation("name", "collection_referenced")
	}
	fields := make([]PlannedField, 0, len(current.Fields))
	for _, field := range current.Fields {
		planned, err := plannedFieldFromCatalog(catalog, field)
		if err != nil {
			return Plan{}, err
		}
		fields = append(fields, planned)
	}
	statements := make([]string, 0, len(current.Fields)+1)
	for _, field := range current.Fields {
		if field.Type != TypeRelation || field.Relation == nil || field.Relation.Storage.Kind != RelationStorageEdgeTableV1 {
			continue
		}
		statements = append(statements, dropMultiRelationStatements(current.TableName, field.Name)...)
	}
	statements = append(statements, "DROP TABLE "+pgx.Identifier{"app", current.TableName}.Sanitize())
	plan := Plan{
		Operation:   deleteCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  PlannedCollection{Name: current.Name, TableName: current.TableName, Fields: fields},
		Statements:  statements,
	}
	checksum, err := planChecksum(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Checksum = checksum
	return plan, nil
}

func plannedFieldFromCatalog(catalog Catalog, field Field) (PlannedField, error) {
	planned := PlannedField{
		Name:     field.Name,
		Type:     field.Type,
		Required: field.Required,
		Unique:   field.Unique,
		Indexed:  field.Indexed,
		Default:  field.Default,
		Ordinal:  field.Ordinal,
	}
	if field.Type != TypeRelation {
		if field.Relation != nil {
			return PlannedField{}, ErrConflict
		}
		return planned, nil
	}
	if field.Relation == nil {
		return PlannedField{}, ErrConflict
	}
	targetName, ok := collectionNameByID(catalog, field.Relation.TargetCollectionID)
	if !ok {
		return PlannedField{}, ErrConflict
	}
	relation := PlannedRelationFromSpec(targetName, *field.Relation)
	planned.Relation = &relation
	return planned, nil
}

func planInputField(
	catalog Catalog,
	sourceCollection string,
	sourceCollectionID uuid.UUID,
	field FieldInput,
	ordinal int,
	creating bool,
) (PlannedField, error) {
	fieldPath := fmt.Sprintf("fields.%d", ordinal)
	if !isSupportedType(field.Type) {
		return PlannedField{}, validation(fieldPath+".type", "unsupported_type")
	}

	var targetCollectionID *uuid.UUID
	storageName := ""
	if field.Type == TypeRelation {
		storageName = relationStorageName(sourceCollection, field)
		if field.Relation != nil {
			targetName := field.Relation.TargetCollection
			switch {
			case creating && targetName == sourceCollection:
				// A create-time self edge stays symbolic until Apply allocates the
				// source collection ID.
			case !creating && targetName == sourceCollection && sourceCollectionID != uuid.Nil:
				target := sourceCollectionID
				targetCollectionID = &target
			default:
				if target, ok := catalog.Collections[targetName]; ok {
					targetID := target.ID
					targetCollectionID = &targetID
				}
			}
		}
	}

	normalized, relation, err := NormalizeRelationField(
		fieldPath,
		sourceCollection,
		field,
		targetCollectionID,
		storageName,
	)
	if err != nil {
		return PlannedField{}, err
	}

	planned := PlannedField{
		Name:     normalized.Name,
		Type:     normalized.Type,
		Required: normalized.Required,
		Unique:   normalized.Unique,
		Indexed:  normalized.Indexed,
		Relation: relation,
		Ordinal:  ordinal,
	}
	if len(normalized.Default) != 0 {
		canonical, _, err := normalizeDefault(normalized.Type, normalized.Default)
		if err != nil {
			return PlannedField{}, validation(fieldPath+".default", "invalid_default")
		}
		planned.Default = append(json.RawMessage(nil), canonical...)
	}
	return planned, nil
}

func relationStorageName(sourceCollection string, field FieldInput) string {
	if field.Relation != nil && field.Relation.Cardinality == RelationMultiple {
		return NewRelationPhysicalNames(sourceCollection, field.Name).MultiTable
	}
	return field.Name
}

func collectionNameByID(catalog Catalog, collectionID uuid.UUID) (string, bool) {
	if collectionID == uuid.Nil {
		return "", false
	}
	for name, collection := range catalog.Collections {
		if collection.ID == collectionID {
			return name, true
		}
	}
	return "", false
}

func collectionHasIncomingRelation(catalog Catalog, targetCollectionID uuid.UUID) bool {
	// validateCatalogIntegrity refuses to compile a catalog whose incoming index
	// disagrees with its collections, so the index alone is authoritative here.
	for _, relation := range catalog.Incoming[targetCollectionID] {
		if relation.SourceCollectionID != targetCollectionID {
			return true
		}
	}
	return false
}

func addColumnStatement(table string, field PlannedField) string {
	column := pgx.Identifier{field.Name}.Sanitize() + " " + string(field.Type)
	if field.Required {
		column += " not null"
	}
	if len(field.Default) != 0 {
		column += " default " + renderValidatedDefault(field)
	}
	return "ALTER TABLE " + table + " ADD COLUMN " + column
}

func alterColumnStatements(table string, ordinal int, previous, field PlannedField) ([]string, error) {
	changes := make([]string, 0, 3)
	if previous.Type != field.Type {
		if !isSupportedTypeChange(previous.Type, field.Type) {
			return nil, validation(fmt.Sprintf("fields.%d.type", ordinal), "unsupported_schema_change")
		}
		column := pgx.Identifier{field.Name}.Sanitize()
		changes = append(changes, "TYPE "+string(field.Type)+" USING "+column+"::"+string(field.Type))
		// Default changes are handled by the shared comparison below so a type
		// change plus default change emits exactly one SET DEFAULT.
	}
	if previous.Required != field.Required {
		if field.Required {
			changes = append(changes, "SET NOT NULL")
		} else {
			changes = append(changes, "DROP NOT NULL")
		}
	}
	if !sameCanonicalDefault(previous.Default, field.Default) {
		if len(field.Default) == 0 {
			changes = append(changes, "DROP DEFAULT")
		} else {
			changes = append(changes, "SET DEFAULT "+renderValidatedDefault(field))
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	column := pgx.Identifier{field.Name}.Sanitize()
	joined := make([]string, 0, len(changes))
	for _, change := range changes {
		joined = append(joined, "ALTER COLUMN "+column+" "+change)
	}
	return []string{"ALTER TABLE " + table + " " + strings.Join(joined, ", ")}, nil
}

func indexChangeStatements(collection, table string, previous, field PlannedField) []string {
	currentHasIndex := previous.Unique || previous.Indexed
	desiredHasIndex := field.Unique || field.Indexed
	uniquenessChanged := previous.Unique != field.Unique
	statements := make([]string, 0, 2)
	if currentHasIndex && (!desiredHasIndex || uniquenessChanged) {
		// The connection search_path does not include "app", so the drop is
		// schema-qualified; IF EXISTS tolerates metadata that drifted ahead
		// of the physical index.
		statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", indexName(collection, previous.Name)}.Sanitize())
	}
	if desiredHasIndex && (!currentHasIndex || uniquenessChanged) {
		prefix := "CREATE INDEX "
		if field.Unique {
			prefix = "CREATE UNIQUE INDEX "
		}
		statements = append(statements, prefix+pgx.Identifier{indexName(collection, field.Name)}.Sanitize()+
			" ON "+table+" ("+pgx.Identifier{field.Name}.Sanitize()+")")
	}
	return statements
}

func isSupportedTypeChange(from, to FieldType) bool {
	switch {
	case from == TypeInteger && to == TypeBigInt,
		from == TypeInteger && to == TypeNumeric,
		from == TypeBigInt && to == TypeNumeric:
		return true
	default:
		return false
	}
}

func sameCanonicalDefault(previous, desired json.RawMessage) bool {
	if len(previous) == 0 && len(desired) == 0 {
		return true
	}
	if len(previous) == 0 || len(desired) == 0 {
		return false
	}
	return bytes.Equal(previous, desired)
}

func renderValidatedDefault(field PlannedField) string {
	_, rendered, err := normalizeDefault(field.Type, field.Default)
	if err != nil {
		panic("validated default became invalid")
	}
	return rendered
}

func isSupportedType(typ FieldType) bool {
	switch typ {
	case TypeBoolean, TypeInteger, TypeBigInt, TypeNumeric, TypeText, TypeTimestampTZ, TypeUUID, TypeJSONB, TypeRelation:
		return true
	default:
		return false
	}
}

func makeCreateStatements(definition PlannedCollection) []string {
	table := pgx.Identifier{"app", definition.TableName}.Sanitize()
	columns := []string{
		`"id" uuid primary key`,
		`"created_at" timestamptz not null`,
		`"updated_at" timestamptz not null`,
	}
	for _, field := range definition.Fields {
		if field.Type == TypeRelation && field.Relation != nil && field.Relation.Cardinality == RelationMultiple {
			continue
		}
		columnType := string(field.Type)
		if field.Type == TypeRelation {
			columnType = string(TypeUUID)
		}
		column := pgx.Identifier{field.Name}.Sanitize() + " " + columnType
		if field.Required {
			column += " not null"
		}
		if len(field.Default) != 0 {
			_, rendered, err := normalizeDefault(field.Type, field.Default)
			if err != nil {
				panic("validated default became invalid")
			}
			column += " default " + rendered
		}
		columns = append(columns, column)
	}

	statements := []string{
		"CREATE TABLE " + table + " (" + strings.Join(columns, ", ") + ")",
		"CREATE INDEX " + pgx.Identifier{indexName(definition.Name, "created_at:id")}.Sanitize() +
			" ON " + table + " (\"created_at\", \"id\")",
	}
	for _, field := range definition.Fields {
		if field.Type == TypeRelation {
			continue
		}
		if !field.Unique && !field.Indexed {
			continue
		}
		prefix := "CREATE INDEX "
		if field.Unique {
			prefix = "CREATE UNIQUE INDEX "
		}
		statements = append(statements, prefix+pgx.Identifier{indexName(definition.Name, field.Name)}.Sanitize()+
			" ON "+table+" ("+pgx.Identifier{field.Name}.Sanitize()+")")
	}
	for _, field := range definition.Fields {
		if field.Type != TypeRelation || field.Relation == nil {
			continue
		}
		statements = append(statements, createRelationStatements(definition.TableName, field, false)...)
	}
	return statements
}

func createRelationStatements(sourceTable string, field PlannedField, addColumn bool) []string {
	if field.Relation == nil {
		return nil
	}
	if field.Relation.Cardinality == RelationMultiple {
		return createMultiRelationStatements(sourceTable, field)
	}
	return createSingleRelationStatements(sourceTable, field, addColumn)
}

func addRelationFieldStatements(sourceTable string, field PlannedField) []string {
	return createRelationStatements(sourceTable, field, true)
}

func createSingleRelationStatements(sourceTable string, field PlannedField, addColumn bool) []string {
	relation := field.Relation
	if relation == nil {
		return nil
	}
	names := NewRelationPhysicalNames(sourceTable, field.Name)
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	target := pgx.Identifier{"app", relation.TargetCollection}.Sanitize()
	column := pgx.Identifier{field.Name}.Sanitize()
	statements := make([]string, 0, 3)
	if addColumn {
		definition := column + " uuid"
		if field.Required {
			definition += " not null"
		}
		statements = append(statements, "ALTER TABLE "+source+" ADD COLUMN "+definition)
	}
	statements = append(statements,
		"ALTER TABLE "+source+" ADD CONSTRAINT "+pgx.Identifier{names.SingleForeignKey}.Sanitize()+
			" FOREIGN KEY ("+column+") REFERENCES "+target+" ("+pgx.Identifier{"id"}.Sanitize()+")"+
			" ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED",
	)
	indexPrefix := "CREATE INDEX "
	if field.Unique {
		indexPrefix = "CREATE UNIQUE INDEX "
	}
	statements = append(statements,
		indexPrefix+pgx.Identifier{names.SingleReverseIndex}.Sanitize()+" ON "+source+" ("+column+")",
	)
	return statements
}

func createMultiRelationStatements(sourceTable string, field PlannedField) []string {
	relation := field.Relation
	if relation == nil {
		return nil
	}
	names := NewRelationPhysicalNames(sourceTable, field.Name)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	target := pgx.Identifier{"app", relation.TargetCollection}.Sanitize()
	sourceID := pgx.Identifier{"source_id"}.Sanitize()
	targetID := pgx.Identifier{"target_id"}.Sanitize()
	position := pgx.Identifier{"position"}.Sanitize()

	createTable := "CREATE TABLE " + edge + " (" +
		sourceID + " uuid NOT NULL, " +
		targetID + " uuid NOT NULL, " +
		position + " integer NOT NULL, " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPrimaryKey}.Sanitize() + " PRIMARY KEY (" + sourceID + ", " + targetID + "), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPositionUnique}.Sanitize() + " UNIQUE (" + sourceID + ", " + position + "), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiPositionCheck}.Sanitize() + " CHECK (" + position + " >= 0), " +
		"CONSTRAINT " + pgx.Identifier{names.MultiSourceForeignKey}.Sanitize() + " FOREIGN KEY (" + sourceID + ")" +
		" REFERENCES " + source + " (" + pgx.Identifier{"id"}.Sanitize() + ") ON DELETE CASCADE, " +
		"CONSTRAINT " + pgx.Identifier{names.MultiTargetForeignKey}.Sanitize() + " FOREIGN KEY (" + targetID + ")" +
		" REFERENCES " + target + " (" + pgx.Identifier{"id"}.Sanitize() + ")" +
		" ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED)"

	return []string{
		createTable,
		"CREATE INDEX " + pgx.Identifier{names.MultiReverseIndex}.Sanitize() + " ON " + edge +
			" (" + targetID + ", " + sourceID + ")",
		positionFunctionStatement(sourceTable, field.Name),
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.PositionTrigger}.Sanitize() +
			" AFTER INSERT OR UPDATE OR DELETE ON " + edge +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize() + "()",
		cardinalityFunctionStatement(sourceTable, field.Name, relation.MinSelect, relation.MaxSelect, false),
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.SourceCardinalityTrigger}.Sanitize() +
			" AFTER INSERT ON " + source +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
		"CREATE CONSTRAINT TRIGGER " + pgx.Identifier{names.EdgeCardinalityTrigger}.Sanitize() +
			" AFTER INSERT OR UPDATE OR DELETE ON " + edge +
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION " +
			pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
	}
}

func positionFunctionStatement(sourceTable, fieldName string) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	function := pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize()
	return "CREATE FUNCTION " + function + `() RETURNS trigger LANGUAGE plpgsql AS $pgbase_relation$
DECLARE
    affected_source uuid;
    affected_sources uuid[];
    relation_count bigint;
    minimum_position integer;
    maximum_position integer;
BEGIN
	IF TG_OP = 'INSERT' THEN
		affected_sources := ARRAY[NEW.source_id];
	ELSIF TG_OP = 'DELETE' THEN
		affected_sources := ARRAY[OLD.source_id];
	ELSE
		affected_sources := ARRAY[OLD.source_id, NEW.source_id];
	END IF;
    FOR affected_source IN
        SELECT DISTINCT candidate
        FROM unnest(affected_sources) AS candidates(candidate)
        WHERE candidate IS NOT NULL
    LOOP
        SELECT count(*), min(position), max(position)
          INTO relation_count, minimum_position, maximum_position
          FROM ` + edge + `
         WHERE source_id = affected_source;
        IF relation_count > 0 AND
           (minimum_position <> 0 OR maximum_position::bigint <> relation_count - 1) THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'relation position constraint violated',
                CONSTRAINT = '` + names.MultiPositionCheck + `';
        END IF;
    END LOOP;
    RETURN NULL;
END
$pgbase_relation$`
}

func cardinalityFunctionStatement(sourceTable, fieldName string, minimum, maximum int, replace bool) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	function := pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize()
	verb := "CREATE FUNCTION "
	if replace {
		verb = "CREATE OR REPLACE FUNCTION "
	}
	return verb + function + `() RETURNS trigger LANGUAGE plpgsql AS $pgbase_relation$
DECLARE
    affected_source uuid;
    affected_sources uuid[];
    relation_count bigint;
BEGIN
    IF TG_TABLE_SCHEMA = 'app' AND TG_TABLE_NAME = ` + postgresStringLiteral(sourceTable) + ` THEN
        affected_source := NEW.id;
        SELECT count(*) INTO relation_count
          FROM ` + edge + `
         WHERE source_id = affected_source;
        IF relation_count < ` + fmt.Sprintf("%d", minimum) + ` OR relation_count > ` + fmt.Sprintf("%d", maximum) + ` THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'relation cardinality constraint violated',
                CONSTRAINT = '` + names.CardinalityFunction + `';
        END IF;
        RETURN NULL;
    END IF;

	IF TG_OP = 'INSERT' THEN
		affected_sources := ARRAY[NEW.source_id];
	ELSIF TG_OP = 'DELETE' THEN
		affected_sources := ARRAY[OLD.source_id];
	ELSE
		affected_sources := ARRAY[OLD.source_id, NEW.source_id];
	END IF;
    FOR affected_source IN
        SELECT DISTINCT candidate
        FROM unnest(affected_sources) AS candidates(candidate)
        WHERE candidate IS NOT NULL
    LOOP
        IF EXISTS (SELECT 1 FROM ` + source + ` WHERE id = affected_source) THEN
            SELECT count(*) INTO relation_count
              FROM ` + edge + `
             WHERE source_id = affected_source;
            IF relation_count < ` + fmt.Sprintf("%d", minimum) + ` OR relation_count > ` + fmt.Sprintf("%d", maximum) + ` THEN
                RAISE EXCEPTION USING
                    ERRCODE = '23514',
                    MESSAGE = 'relation cardinality constraint violated',
                    CONSTRAINT = '` + names.CardinalityFunction + `';
            END IF;
        END IF;
    END LOOP;
    RETURN NULL;
END
$pgbase_relation$`
}

func postgresStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func dropRelationFieldStatements(collection Collection, field Field) []string {
	if field.Relation == nil {
		return nil
	}
	if field.Relation.Storage.Kind == RelationStorageEdgeTableV1 {
		return dropMultiRelationStatements(collection.TableName, field.Name)
	}
	names := NewRelationPhysicalNames(collection.TableName, field.Name)
	table := pgx.Identifier{"app", collection.TableName}.Sanitize()
	return []string{
		"ALTER TABLE " + table + " DROP CONSTRAINT " + pgx.Identifier{names.SingleForeignKey}.Sanitize(),
		"DROP INDEX IF EXISTS " + pgx.Identifier{"app", names.SingleReverseIndex}.Sanitize(),
		"ALTER TABLE " + table + " DROP COLUMN " + pgx.Identifier{field.ColumnName}.Sanitize(),
	}
}

func dropMultiRelationStatements(sourceTable, fieldName string) []string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	return []string{
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.PositionTrigger}.Sanitize() + " ON " + edge,
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.EdgeCardinalityTrigger}.Sanitize() + " ON " + edge,
		"DROP TRIGGER IF EXISTS " + pgx.Identifier{names.SourceCardinalityTrigger}.Sanitize() + " ON " + source,
		"DROP TABLE " + edge,
		"DROP FUNCTION " + pgx.Identifier{"pgbase_relation", names.PositionFunction}.Sanitize() + "()",
		"DROP FUNCTION " + pgx.Identifier{"pgbase_relation", names.CardinalityFunction}.Sanitize() + "()",
	}
}

func alterRelationFieldStatements(sourceTable string, ordinal int, previous, desired PlannedField) ([]string, error) {
	fieldPath := fmt.Sprintf("fields.%d", ordinal)
	if previous.Type != TypeRelation || desired.Type != TypeRelation || previous.Relation == nil || desired.Relation == nil {
		return nil, validation(fieldPath+".type", "relation_storage_migration_required")
	}
	before := previous.Relation
	after := desired.Relation
	if !samePlannedRelationTarget(before, after) ||
		before.Cardinality != after.Cardinality ||
		before.ValueCodecVersion != after.ValueCodecVersion ||
		before.Storage != after.Storage {
		return nil, validation(fieldPath+".relation", "relation_storage_migration_required")
	}

	if after.Cardinality == RelationSingle {
		statements := make([]string, 0, 3)
		table := pgx.Identifier{"app", sourceTable}.Sanitize()
		column := pgx.Identifier{desired.Name}.Sanitize()
		if previous.Required != desired.Required {
			action := "DROP NOT NULL"
			if desired.Required {
				action = "SET NOT NULL"
			}
			statements = append(statements, "ALTER TABLE "+table+" ALTER COLUMN "+column+" "+action)
		}
		if previous.Unique != desired.Unique {
			names := NewRelationPhysicalNames(sourceTable, desired.Name)
			statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", names.SingleReverseIndex}.Sanitize())
			prefix := "CREATE INDEX "
			if desired.Unique {
				prefix = "CREATE UNIQUE INDEX "
			}
			statements = append(statements, prefix+pgx.Identifier{names.SingleReverseIndex}.Sanitize()+" ON "+table+" ("+column+")")
		}
		return statements, nil
	}

	if before.MinSelect == after.MinSelect && before.MaxSelect == after.MaxSelect {
		return nil, nil
	}
	return []string{
		validateMultiBoundsStatement(sourceTable, desired.Name, after.MinSelect, after.MaxSelect),
		cardinalityFunctionStatement(sourceTable, desired.Name, after.MinSelect, after.MaxSelect, true),
	}, nil
}

func samePlannedRelationTarget(left, right *PlannedRelation) bool {
	if left == nil || right == nil || left.TargetCollectionID == nil || right.TargetCollectionID == nil {
		return left != nil && right != nil && left.TargetCollectionID == nil && right.TargetCollectionID == nil &&
			left.TargetCollection == right.TargetCollection
	}
	return *left.TargetCollectionID == *right.TargetCollectionID
}

func validateMultiBoundsStatement(sourceTable, fieldName string, minimum, maximum int) string {
	names := NewRelationPhysicalNames(sourceTable, fieldName)
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	source := pgx.Identifier{"app", sourceTable}.Sanitize()
	return `DO $pgbase_relation$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM ` + source + ` AS source_record
          LEFT JOIN ` + edge + ` AS relation_edge
            ON relation_edge.source_id = source_record.id
         GROUP BY source_record.id
        HAVING count(relation_edge.target_id) < ` + fmt.Sprintf("%d", minimum) + `
            OR count(relation_edge.target_id) > ` + fmt.Sprintf("%d", maximum) + `
    ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            MESSAGE = 'existing data violates relation cardinality';
    END IF;
END
$pgbase_relation$`
}

func indexName(collection, field string) string {
	digest := sha256.Sum256([]byte(collection + ":" + field))
	name := "idx_" + hex.EncodeToString(digest[:])[:indexHashLength]
	if len(name) > postgresIdentifierLimit {
		return name[:postgresIdentifierLimit]
	}
	return name
}

const schemaPlanCompilerVersion = "schema-plan-v1"

func planChecksum(plan Plan) (string, error) {
	return planChecksumWithCompilerVersion(plan, schemaPlanCompilerVersion)
}

func planChecksumWithCompilerVersion(plan Plan, compilerVersion string) (string, error) {
	if compilerVersion == "" {
		return "", errors.New("schema plan compiler version is required")
	}
	definition := plan.Definition
	definition.Fields = append([]PlannedField(nil), definition.Fields...)
	sort.Slice(definition.Fields, func(i, j int) bool {
		if definition.Fields[i].Ordinal != definition.Fields[j].Ordinal {
			return definition.Fields[i].Ordinal < definition.Fields[j].Ordinal
		}
		return definition.Fields[i].Name < definition.Fields[j].Name
	})
	canonical := struct {
		CompilerVersion string
		Operation       string
		BaseVersion     int64
		Definition      PlannedCollection
		Statements      []string
	}{
		CompilerVersion: compilerVersion,
		Operation:       plan.Operation,
		BaseVersion:     plan.BaseVersion,
		Definition:      definition,
		Statements:      plan.Statements,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal schema plan checksum: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
