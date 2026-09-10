package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var compatibilityCollectionName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func normalizeCreateCollectionMetadata(input *CreateCollectionInput) error {
	if input.Type == "" {
		if input.View != nil {
			input.Type = CollectionTypeView
		} else {
			input.Type = CollectionTypeBase
		}
	}
	if input.ValidationMode == "" {
		input.ValidationMode = ValidationModePostgres
	}
	if input.CompatibilityName != "" && (len(input.CompatibilityName) > 255 || !compatibilityCollectionName.MatchString(input.CompatibilityName)) {
		return validation("compatibilityName", "invalid_name")
	}
	if input.Type != CollectionTypeBase && input.Type != CollectionTypeAuth && input.Type != CollectionTypeView {
		return validation("type", "invalid")
	}
	if input.ValidationMode != ValidationModePostgres && input.ValidationMode != ValidationModePocketBase {
		return validation("validationMode", "invalid")
	}
	if (input.View != nil) != (input.Type == CollectionTypeView) {
		return validation("type", "view_mismatch")
	}
	return nil
}

const (
	createCollectionOperation = "collection_create"
	updateCollectionOperation = "collection_update"
	deleteCollectionOperation = "collection_delete"
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
		current, ok := collectionForUpdate(catalog, change.update)
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
	if err := normalizeCreateCollectionMetadata(&input); err != nil {
		return Plan{}, err
	}
	if err := validateInputName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if input.View != nil {
		return planViewCreate(catalog, input)
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	fields := make([]PlannedField, 0, len(input.Fields))
	seenNames := make(map[string]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if field.ID != nil {
			return Plan{}, validation(fmt.Sprintf("fields.%d.id", ordinal), "not_allowed")
		}
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

	indexes, err := planCompoundIndexes(input.Name, input.Indexes, fields)
	if err != nil {
		return Plan{}, err
	}
	definition := PlannedCollection{Name: input.Name, CompatibilityName: input.CompatibilityName, Type: input.Type, System: input.System, ValidationMode: input.ValidationMode, TableName: input.Name, Fields: fields, Indexes: indexes}
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
	if input.CollectionID == nil && current.Name != input.Name {
		return Plan{}, ErrConflict
	}
	if input.CollectionID != nil && *input.CollectionID != current.ID {
		return Plan{}, ErrConflict
	}
	if err := validateInputName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if existing, exists := catalog.Collections[input.Name]; exists && existing.ID != current.ID {
		return Plan{}, validation("name", "duplicate_name")
	}
	if current.IsView() {
		if plan, handled, err := planViewRename(catalog, current, input); handled {
			return plan, err
		}
		if current.IsSQLView() && input.View != nil && input.View.SQL != "" {
			return planSQLViewUpdate(catalog, current, input)
		}
		if !current.IsSQLView() && input.View != nil && input.View.SQL == "" {
			return planDeclarativeViewUpdate(catalog, current, input)
		}
		return Plan{}, validation("name", "view_read_only")
	}
	if input.View != nil {
		return Plan{}, validation("view", "not_allowed")
	}
	if current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	if input.Type != nil && *input.Type != current.Type {
		return Plan{}, validation("type", "immutable")
	}
	if input.System != nil && *input.System != current.System {
		return Plan{}, validation("system", "immutable")
	}
	compatibilityName := current.CompatibilityName
	if input.CompatibilityName != nil {
		compatibilityName = *input.CompatibilityName
	}
	if compatibilityName != "" && (len(compatibilityName) > 255 || !compatibilityCollectionName.MatchString(compatibilityName)) {
		return Plan{}, validation("compatibilityName", "invalid_name")
	}
	validationMode := current.ValidationMode
	if validationMode == "" {
		validationMode = ValidationModePostgres
	}
	if input.ValidationMode != nil {
		validationMode = *input.ValidationMode
	}
	if validationMode != ValidationModePostgres && validationMode != ValidationModePocketBase {
		return Plan{}, validation("validationMode", "invalid")
	}
	if input.Fields == nil && input.CollectionID != nil {
		var err error
		input.Fields, err = updateFieldsFromCatalog(catalog, current)
		if err != nil {
			return Plan{}, err
		}
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	currentFields := make(map[string]PlannedField, len(current.Fields))
	currentFieldsByID := make(map[uuid.UUID]PlannedField, len(current.Fields))
	currentColumns := make(map[string]PlannedField, len(current.Fields))
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
		currentFieldsByID[planned.ID] = planned
		currentColumns[planned.ColumnName] = planned
	}

	desired := make([]PlannedField, 0, len(input.Fields))
	desiredNames := make(map[string]struct{}, len(input.Fields))
	desiredIDs := make(map[uuid.UUID]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if err := validateInputName(namePath, field.Name); err != nil {
			return Plan{}, err
		}
		if _, duplicate := desiredNames[field.Name]; duplicate {
			return Plan{}, validation(namePath, "duplicate_name")
		}
		desiredNames[field.Name] = struct{}{}

		previous, exists := currentFields[field.Name]
		if field.ID == nil && exists {
			if _, alreadyClaimed := desiredIDs[previous.ID]; alreadyClaimed {
				exists = false
				previous = PlannedField{}
			}
		}
		if field.ID != nil {
			previous, exists = currentFieldsByID[*field.ID]
			if !exists {
				return Plan{}, validation(namePath, "unknown_id")
			}
			if occupied, ok := currentFields[field.Name]; ok && occupied.ID != previous.ID {
				return Plan{}, validation(namePath, "rename_target_exists")
			}
		}
		if exists && previous.ID != uuid.Nil {
			if _, duplicate := desiredIDs[previous.ID]; duplicate {
				return Plan{}, validation(namePath, "duplicate_id")
			}
		}
		carriedDefault := json.RawMessage(nil)
		if exists {
			carriedDefault = unchangedStoredDefault(previous, field)
		}
		if len(carriedDefault) != 0 {
			field.Default = nil
		}
		planned, err := planInputField(catalog, current.Name, current.ID, field, ordinal, false)
		if err != nil {
			return Plan{}, err
		}
		if !exists {
			if _, occupied := currentColumns[planned.ColumnName]; occupied {
				planned.ColumnName = reusablePhysicalColumn(current.Name, field.Name, currentColumns)
				if planned.Relation != nil {
					planned.Relation.Storage.Name = planned.ColumnName
					if planned.Relation.Cardinality == RelationMultiple {
						planned.Relation.Storage.Name = NewRelationPhysicalNames(current.TableName, planned.ColumnName).MultiTable
					}
				}
			}
			currentColumns[planned.ColumnName] = planned
		}
		if len(carriedDefault) != 0 {
			planned.Default = append(json.RawMessage(nil), carriedDefault...)
		}
		if exists {
			if previous.System && (field.System == nil || !*field.System || field.Name != previous.Name) {
				return Plan{}, validation(namePath, "system_field_immutable")
			}
			if !previous.System && field.System != nil && *field.System {
				return Plan{}, validation(namePath, "system_immutable")
			}
			planned.ID = previous.ID
			planned.ColumnName = previous.ColumnName
			if previous.ID != uuid.Nil {
				desiredIDs[previous.ID] = struct{}{}
			}
			if field.Options == nil {
				planned.Options = cloneFieldOptions(previous.Options)
			}
			if previous.Relation != nil && planned.Relation != nil {
				planned.Relation.Storage = previous.Relation.Storage
			}
		}
		desired = append(desired, planned)
	}
	if err := validateViewSourceUpdate(catalog, current.ID, currentFields, desired); err != nil {
		return Plan{}, err
	}

	table := pgx.Identifier{"app", current.TableName}.Sanitize()
	statements := make([]string, 0, len(desired))
	for _, field := range current.Fields {
		if _, kept := desiredIDs[field.ID]; kept {
			continue
		}
		if _, kept := desiredNames[field.Name]; kept {
			continue
		}
		if field.System {
			return Plan{}, validation("fields", "system_field_immutable")
		}
		if field.Type == TypeRelation && field.Relation != nil {
			statements = append(statements, dropRelationFieldStatements(current, field)...)
			continue
		}
		if field.Type == TypeFile {
			continue
		}
		statements = append(statements, "ALTER TABLE "+table+" DROP COLUMN "+pgx.Identifier{field.ColumnName}.Sanitize())
	}
	for ordinal, field := range desired {
		previous, exists := currentFields[field.Name]
		if field.ID != uuid.Nil {
			previous, exists = currentFieldsByID[field.ID]
		} else if exists {
			if _, claimed := desiredIDs[previous.ID]; claimed {
				previous, exists = PlannedField{}, false
			}
		}
		if !exists {
			if field.Type == TypeFile {
				continue
			}
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
				statements = append(statements, createRelationStatements(current.TableName, field, true)...)
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
		if previous.Type == TypeFile || field.Type == TypeFile {
			if previous.Type != field.Type {
				return Plan{}, validation(fmt.Sprintf("fields.%d.type", ordinal), "unsupported_schema_change")
			}
			continue
		}
		alter, err := alterColumnStatements(table, ordinal, previous, field)
		if err != nil {
			return Plan{}, err
		}
		statements = append(statements, alter...)
		statements = append(statements, indexChangeStatements(current.Name, table, previous, field)...)
	}
	var indexes []PlannedIndex
	if input.Indexes == nil {
		preserved := make([]IndexInput, 0, len(current.Indexes))
		for _, index := range current.Indexes {
			preserved = append(preserved, indexInputForCollection(index, input.Name))
		}
		var err error
		indexes, err = planCompoundIndexes(input.Name, preserved, desired)
		if err != nil {
			return Plan{}, validation("indexes", "referenced_field_changed")
		}
	} else {
		var err error
		indexes, err = planCompoundIndexes(input.Name, *input.Indexes, desired)
		if err != nil {
			return Plan{}, err
		}
	}
	comparisonIndexes := current.Indexes
	if input.Name != current.Name {
		comparisonIndexes = catalogIndexesForCollection(current.Indexes, input.Name)
	}
	statements = append(statements, compoundIndexChangeStatements(current.Name, current.TableName, comparisonIndexes, indexes, desired)...)
	if input.Name != current.Name {
		if len(statements) != 0 {
			return Plan{}, validation("name", "rename_requires_unchanged_definition")
		}
		renameStatements, renamedFields, err := renameCollectionStatements(current, input.Name, desired, indexes)
		if err != nil {
			return Plan{}, err
		}
		desired = renamedFields
		statements = append(statements, renameStatements...)
		for index := range desired {
			if desired[index].Relation != nil && desired[index].Relation.TargetCollectionID != nil && *desired[index].Relation.TargetCollectionID == current.ID {
				desired[index].Relation.TargetCollection = input.Name
			}
		}
	}

	return Plan{
		Operation:   updateCollectionOperation,
		BaseVersion: catalog.Version,
		Definition:  PlannedCollection{Name: input.Name, CompatibilityName: compatibilityName, Type: current.Type, System: current.System, ValidationMode: validationMode, TableName: input.Name, Fields: desired, Indexes: indexes},
		Statements:  statements,
	}, nil
}

func reusablePhysicalColumn(collection, logical string, occupied map[string]PlannedField) string {
	for salt := 0; ; salt++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("pgbase:reused-column:v1:%s:%s:%d", collection, logical, salt)))
		candidate := "pgb_" + hex.EncodeToString(digest[:])[:20]
		if _, exists := occupied[candidate]; !exists {
			return candidate
		}
	}
}

// Dependent indexes are dropped with the table.
func planDelete(catalog Catalog, current Collection) (Plan, error) {
	if current.System {
		return Plan{}, validation("system", "immutable")
	}
	if current.Name == "" || current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	if collectionHasIncomingRelation(catalog, current.ID) {
		return Plan{}, validation("name", "collection_referenced")
	}
	for _, candidate := range catalog.Collections {
		if viewReferencesCollection(candidate, current.ID) {
			return Plan{}, validation("name", "collection_referenced")
		}
	}
	if current.View != nil {
		return Plan{Operation: deleteCollectionOperation, BaseVersion: catalog.Version, Definition: PlannedCollection{Name: current.Name, TableName: current.TableName, View: &PlannedView{SourceCollectionID: current.View.SourceCollectionID, Source: current.View.Source, Projection: append([]string(nil), current.View.Projection...), Filter: cloneViewFilter(current.View.Filter), SQL: current.View.SQL, Dependencies: append([]uuid.UUID(nil), current.View.Dependencies...)}}, Statements: []string{"DROP VIEW " + pgx.Identifier{"app", current.TableName}.Sanitize()}}, nil
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
		// ColumnName is the persisted physical identity across logical renames.
		// Storage.Name identifies the edge table produced from that identity; the
		// remaining owned object names are derived from the same physical name.
		if NewRelationPhysicalNames(current.TableName, field.ColumnName).MultiTable != field.Relation.Storage.Name {
			return Plan{}, ErrConflict
		}
		statements = append(statements, dropMultiRelationStatements(current.TableName, field.ColumnName)...)
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
		ID: field.ID, ColumnName: field.ColumnName,
		Name:     field.Name,
		Type:     field.Type,
		Required: field.Required,
		Unique:   field.Unique,
		Indexed:  field.Indexed,
		Default:  field.Default,
		Ordinal:  field.Ordinal,
		Options:  cloneFieldOptions(field.Options),
		Hidden:   field.Hidden, Presentable: field.Presentable, Help: field.Help,
		System: field.System,
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
	if field.Options != nil {
		options := cloneFieldOptions(*field.Options)
		field.Options = &options
	}
	if err := normalizeFieldOptionsInput(field.Options); err != nil {
		return PlannedField{}, validation(fieldPath+".options", "invalid_options")
	}
	if len([]rune(field.Help)) > 300 {
		return PlannedField{}, validation(fieldPath+".help", "too_long")
	}
	if field.Hidden && field.Presentable {
		return PlannedField{}, validation(fieldPath+".presentable", "hidden_field")
	}
	if err := validateFieldOptions(field.Type, field.Options); err != nil {
		return PlannedField{}, validation(fieldPath+".options", "invalid_options")
	}
	if !isSupportedType(field.Type) {
		return PlannedField{}, validation(fieldPath+".type", "unsupported_type")
	}
	if field.Type == TypeFile && (field.Unique || field.Indexed || len(field.Default) != 0 || field.Relation != nil) {
		return PlannedField{}, validation(fieldPath, "invalid_file_field")
	}
	if code := validateAdvancedFieldInput(field); code != "" {
		return PlannedField{}, validation(fieldPath, code)
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
					if target.IsView() {
						return PlannedField{}, validation(fieldPath+".relation.targetCollection", "view_not_supported")
					}
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
		ColumnName: normalized.Name,
		Name:       normalized.Name,
		Type:       normalized.Type,
		Required:   normalized.Required,
		Unique:     normalized.Unique,
		Indexed:    normalized.Indexed,
		Relation:   relation,
		Ordinal:    ordinal,
		Options:    clonedInputOptions(normalized.Options),
		Hidden:     normalized.Hidden, Presentable: normalized.Presentable, Help: normalized.Help,
		System: normalized.System != nil && *normalized.System,
	}
	if normalized.Type == TypeEditor {
		planned.Default = json.RawMessage(`""`)
	}
	if normalized.Type == TypeGeoPoint {
		planned.Default = json.RawMessage(`{"lat":0,"lon":0}`)
	}
	if len(normalized.Default) != 0 {
		canonical, _, err := normalizeDefault(normalized.Type, normalized.Default)
		if err != nil {
			return PlannedField{}, validation(fieldPath+".default", "invalid_default")
		}
		planned.Default = append(json.RawMessage(nil), canonical...)
		if !defaultSatisfiesOptions(planned.Type, canonical, planned.Options) {
			return PlannedField{}, validation(fieldPath+".default", "invalid_default")
		}
	}
	return planned, nil
}

func addColumnStatement(table string, field PlannedField) string {
	column := pgx.Identifier{field.ColumnName}.Sanitize() + " " + physicalFieldType(field.Type)
	if physicalFieldNotNull(field) {
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
		column := pgx.Identifier{field.ColumnName}.Sanitize()
		changes = append(changes, "TYPE "+physicalFieldType(field.Type)+" USING "+column+"::"+physicalFieldType(field.Type))
		// Default changes are handled by the shared comparison below so a type
		// change plus default change emits exactly one SET DEFAULT.
	}
	if physicalFieldNotNull(previous) != physicalFieldNotNull(field) {
		if physicalFieldNotNull(field) {
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
	column := pgx.Identifier{field.ColumnName}.Sanitize()
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
	renamed := previous.ID != uuid.Nil && previous.Name != field.Name
	statements := make([]string, 0, 2)
	if currentHasIndex && (!desiredHasIndex || uniquenessChanged || renamed) {
		// The connection search_path does not include "app", so the drop is
		// schema-qualified; IF EXISTS tolerates metadata that drifted ahead
		// of the physical index.
		statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", indexName(collection, previous.Name)}.Sanitize())
	}
	if desiredHasIndex && (!currentHasIndex || uniquenessChanged || renamed) {
		prefix := "CREATE INDEX "
		if field.Unique {
			prefix = "CREATE UNIQUE INDEX "
		}
		statements = append(statements, prefix+pgx.Identifier{indexName(collection, field.Name)}.Sanitize()+
			" ON "+table+" ("+pgx.Identifier{field.ColumnName}.Sanitize()+")")
	}
	return statements
}

func renderValidatedDefault(field PlannedField) string {
	_, rendered, err := normalizeDefault(field.Type, field.Default)
	if err != nil {
		panic("validated default became invalid")
	}
	return rendered
}

func makeCreateStatements(definition PlannedCollection, companions bool) []string {
	table := pgx.Identifier{"app", definition.TableName}.Sanitize()
	columns := []string{
		`"id" uuid primary key`,
		`"created_at" timestamptz not null`,
		`"updated_at" timestamptz not null`,
	}
	for _, field := range definition.Fields {
		if field.Type == TypeFile {
			continue
		}
		if field.Type == TypeRelation && field.Relation != nil && field.Relation.Cardinality == RelationMultiple {
			continue
		}
		columnType := physicalFieldType(field.Type)
		if field.Type == TypeRelation {
			columnType = string(TypeUUID)
		}
		column := pgx.Identifier{field.Name}.Sanitize() + " " + columnType
		if physicalFieldNotNull(field) {
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
	for _, index := range definition.Indexes {
		statements = append(statements, createCompoundIndexStatement(definition.Name, definition.TableName, index, definition.Fields))
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
