package schema

import (
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
	postgresIdentifierLimit   = 63
	indexHashLength           = 16
)

// PlanCreate validates a collection definition and produces its deterministic DDL plan.
func (Planner) PlanCreate(baseVersion int64, input CreateCollectionInput) (Plan, error) {
	if err := validateName("name", input.Name); err != nil {
		return Plan{}, err
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

	statements := []string{"CREATE TABLE " + table + " (" + strings.Join(columns, ", ") + ")"}
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
