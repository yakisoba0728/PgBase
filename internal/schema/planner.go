package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// PlanCreate validates a collection definition and produces its deterministic DDL plan.
func (Planner) PlanCreate(baseVersion int64, input CreateCollectionInput) (Plan, error) {
	if err := validateName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	fields := make([]PlannedField, 0, len(input.Fields))
	seenNames := make(map[string]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if err := validateName(namePath, field.Name); err != nil {
			return Plan{}, err
		}
		if _, duplicate := seenNames[field.Name]; duplicate {
			return Plan{}, validation(namePath, "duplicate_name")
		}
		seenNames[field.Name] = struct{}{}
		if !isSupportedType(field.Type) {
			return Plan{}, validation(fmt.Sprintf("fields.%d.type", ordinal), "unsupported_type")
		}

		planned := PlannedField{
			Name:     field.Name,
			Type:     field.Type,
			Required: field.Required,
			Unique:   field.Unique,
			Indexed:  field.Indexed,
			Ordinal:  ordinal,
		}
		if len(field.Default) != 0 {
			canonical, _, err := normalizeDefault(field.Type, field.Default)
			if err != nil {
				return Plan{}, validation(fmt.Sprintf("fields.%d.default", ordinal), "invalid_default")
			}
			planned.Default = append(json.RawMessage(nil), canonical...)
		}
		fields = append(fields, planned)
	}

	definition := PlannedCollection{Name: input.Name, TableName: input.Name, Fields: fields}
	statements := makeCreateStatements(definition)
	plan := Plan{
		Operation:   createCollectionOperation,
		BaseVersion: baseVersion,
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

// PlanUpdate diffs the desired field list against the current catalog state and
// produces its deterministic DDL plan. Field renames are not supported: a name
// that disappears is a drop and a name that appears is an add.
func (Planner) PlanUpdate(baseVersion int64, current Collection, input UpdateCollectionInput) (Plan, error) {
	if err := validateName("name", input.Name); err != nil {
		return Plan{}, err
	}
	if input.Name != current.Name || current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	if len(input.Fields) > maximumCollectionFields {
		return Plan{}, validation("fields", "too_many_fields")
	}

	desired := make([]PlannedField, 0, len(input.Fields))
	desiredNames := make(map[string]struct{}, len(input.Fields))
	for ordinal, field := range input.Fields {
		namePath := fmt.Sprintf("fields.%d.name", ordinal)
		if err := validateName(namePath, field.Name); err != nil {
			return Plan{}, err
		}
		if _, duplicate := desiredNames[field.Name]; duplicate {
			return Plan{}, validation(namePath, "duplicate_name")
		}
		desiredNames[field.Name] = struct{}{}
		if !isSupportedType(field.Type) {
			return Plan{}, validation(fmt.Sprintf("fields.%d.type", ordinal), "unsupported_type")
		}

		planned := PlannedField{
			Name:     field.Name,
			Type:     field.Type,
			Required: field.Required,
			Unique:   field.Unique,
			Indexed:  field.Indexed,
			Ordinal:  ordinal,
		}
		if len(field.Default) != 0 {
			canonical, _, err := normalizeDefault(field.Type, field.Default)
			if err != nil {
				return Plan{}, validation(fmt.Sprintf("fields.%d.default", ordinal), "invalid_default")
			}
			planned.Default = append(json.RawMessage(nil), canonical...)
		}
		desired = append(desired, planned)
	}

	currentFields := make(map[string]PlannedField, len(current.Fields))
	for _, field := range current.Fields {
		planned := plannedFieldFrom(field)
		// Defaults are stored in a jsonb column whose text rendering differs
		// from the Go-canonical form; re-normalize so an unchanged object or
		// "now" default does not re-emit SET DEFAULT on every update.
		if len(planned.Default) != 0 {
			if canonical, _, err := normalizeDefault(planned.Type, planned.Default); err == nil {
				planned.Default = canonical
			}
		}
		currentFields[planned.Name] = planned
	}

	table := pgx.Identifier{"app", current.TableName}.Sanitize()
	statements := make([]string, 0, len(desired))
	for _, field := range current.Fields {
		if _, kept := desiredNames[field.Name]; kept {
			continue
		}
		statements = append(statements, "ALTER TABLE "+table+" DROP COLUMN "+pgx.Identifier{field.Name}.Sanitize())
	}
	for ordinal, field := range desired {
		previous, exists := currentFields[field.Name]
		if !exists {
			if field.Required && len(field.Default) == 0 {
				return Plan{}, validation(fmt.Sprintf("fields.%d.required", ordinal), "missing_default")
			}
			statements = append(statements, addColumnStatement(table, field))
			// A brand-new field has no prior index state, so its flags must
			// produce the same CREATE (UNIQUE) INDEX that PlanCreate emits.
			statements = append(statements, indexChangeStatements(current.Name, table, PlannedField{}, field)...)
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
		BaseVersion: baseVersion,
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

// PlanDelete produces the deterministic plan that removes a collection and its
// physical table. Dependent indexes are dropped with the table.
func (Planner) PlanDelete(baseVersion int64, current Collection) (Plan, error) {
	if current.Name == "" || current.TableName != current.Name {
		return Plan{}, ErrConflict
	}
	fields := make([]PlannedField, 0, len(current.Fields))
	for _, field := range current.Fields {
		fields = append(fields, plannedFieldFrom(field))
	}
	plan := Plan{
		Operation:   deleteCollectionOperation,
		BaseVersion: baseVersion,
		Definition:  PlannedCollection{Name: current.Name, TableName: current.TableName, Fields: fields},
		Statements:  []string{"DROP TABLE " + pgx.Identifier{"app", current.TableName}.Sanitize()},
	}
	checksum, err := planChecksum(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Checksum = checksum
	return plan, nil
}

func plannedFieldFrom(field Field) PlannedField {
	return PlannedField{
		Name:     field.Name,
		Type:     field.Type,
		Required: field.Required,
		Unique:   field.Unique,
		Indexed:  field.Indexed,
		Default:  field.Default,
		Ordinal:  field.Ordinal,
	}
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
	case TypeBoolean, TypeInteger, TypeBigInt, TypeNumeric, TypeText, TypeTimestampTZ, TypeUUID, TypeJSONB:
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
		column := pgx.Identifier{field.Name}.Sanitize() + " " + string(field.Type)
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

func planChecksum(plan Plan) (string, error) {
	definition := plan.Definition
	definition.Fields = append([]PlannedField(nil), definition.Fields...)
	sort.Slice(definition.Fields, func(i, j int) bool {
		if definition.Fields[i].Ordinal != definition.Fields[j].Ordinal {
			return definition.Fields[i].Ordinal < definition.Fields[j].Ordinal
		}
		return definition.Fields[i].Name < definition.Fields[j].Name
	})
	canonical := struct {
		Operation   string
		BaseVersion int64
		Definition  PlannedCollection
		Statements  []string
	}{
		Operation:   plan.Operation,
		BaseVersion: plan.BaseVersion,
		Definition:  definition,
		Statements:  plan.Statements,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal schema plan checksum: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
