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

// CompilePlan compiles one change in the pre-companion v1 format, for callers
// holding a catalog and no database. Production issuance goes through
// compilePlan: D2-2 makes the capability row the only source of that format.
func CompilePlan(catalog Catalog, change Change) (Plan, error) {
	return compilePlan(catalog, change, false)
}

// compilePlan stamps the generation once, here, so no plan is built in one
// format and labelled the other. Only a create's statements differ; every
// plan's checksum does.
func compilePlan(catalog Catalog, change Change, companions bool) (Plan, error) {
	var plan Plan
	var err error
	switch change.kind {
	case changeCreate:
		plan, err = planCreate(catalog, change.create, companions)
	case changeUpdate:
		current, ok := catalog.Collections[change.update.Name]
		if !ok {
			return Plan{}, ErrCollectionNotFound
		}
		plan, err = planUpdate(catalog, current, change.update)
	case changeDelete:
		current, ok := catalog.Collections[change.deleteName]
		if !ok {
			return Plan{}, ErrCollectionNotFound
		}
		plan, err = planDelete(catalog, current)
	default:
		return Plan{}, ErrConflict
	}
	if err != nil {
		return Plan{}, err
	}
	compilerVersion := schemaPlanCompilerV1
	if companions {
		compilerVersion = schemaPlanCompilerV2
	}
	plan.Checksum, err = planChecksumWithCompilerVersion(plan, compilerVersion)
	return plan, err
}

// compileMixedFleetPlan compiles the plan this binary would issue and, beside it,
// the plan the other live generation produces for the same change. R5 requires dual
// validation across the mixed-fleet window: a preview from a writer at the other
// generation, or from this one before an activation moved the capability, must still
// apply. Both carry the same definition, operation and base version, so neither admits
// a catalog change the other would refuse — but their statement lists differ, which is
// the whole point of the v2 generation. So the apply must execute the plan the caller's
// checksum names, not this binary's preference; both are returned whole so it can.
func compileMixedFleetPlan(catalog Catalog, change Change, companions bool) (Plan, Plan, error) {
	issued, err := compilePlan(catalog, change, companions)
	if err != nil {
		return Plan{}, Plan{}, err
	}
	alternate, err := compilePlan(catalog, change, !companions)
	return issued, alternate, err
}

func planCreate(catalog Catalog, input CreateCollectionInput, companions bool) (Plan, error) {
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
	return Plan{
		Operation:   createCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  definition,
		Statements:  makeCreateStatements(definition, companions),
	}, nil
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

	return Plan{
		Operation:   updateCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  PlannedCollection{Name: input.Name, TableName: current.TableName, Fields: desired},
		Statements:  statements,
	}, nil
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
	return Plan{
		Operation:   deleteCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  PlannedCollection{Name: current.Name, TableName: current.TableName, Fields: fields},
		Statements:  statements,
	}, nil
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

func makeCreateStatements(definition PlannedCollection, companions bool) []string {
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
	// D2-4: a collection created while the capability is ready gets its companions here,
	// in the apply transaction that creates the table, so the capability it was created
	// under still describes the whole catalog and no operator step stands between the
	// create and a nullable descending page. CONCURRENTLY is illegal in a transaction
	// block and needless: the table is empty and unreadable until this commits.
	for _, field := range definition.Fields {
		if !companions || !nullableDescSortable(field.Required, field.Indexed || field.Unique,
			field.Relation != nil && field.Relation.Cardinality == RelationSingle, field.Type) {
			continue
		}
		statements = append(statements, createCompanionIndexStatement(
			nullableDescCompanionSpec(definition.Name, definition.TableName, field.Name, field.Name), false))
	}
	return statements
}

func indexName(collection, field string) string {
	digest := sha256.Sum256([]byte(collection + ":" + field))
	name := "idx_" + hex.EncodeToString(digest[:])[:indexHashLength]
	if len(name) > postgresIdentifierLimit {
		return name[:postgresIdentifierLimit]
	}
	return name
}

// The two plan formats a live fleet can hold. v2 differs from v1 in exactly one
// way — a create plan builds its companion indexes in the same transaction as
// the table — and the label is part of the digest, so the two never collide.
// Which one this binary issues is not a constant: migration 8 puts that in the
// capability row, where only 'ready' means v2.
const (
	schemaPlanCompilerV1 = "schema-plan-v1"
	schemaPlanCompilerV2 = "schema-plan-v2"
)

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
