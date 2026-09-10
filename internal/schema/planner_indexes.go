package schema

import (
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
)

func planCompoundIndexes(inputs []IndexInput, fields []PlannedField) ([]PlannedIndex, error) {
	available := make(map[string]PlannedField, len(fields))
	for _, field := range fields {
		available[field.Name] = field
	}
	seen := make(map[string]struct{}, len(inputs))
	seenFields := make(map[string]struct{}, len(inputs))
	result := make([]PlannedIndex, 0, len(inputs))
	for i, input := range inputs {
		path := fmt.Sprintf("indexes.%d", i)
		if err := validateInputName(path+".name", input.Name); err != nil {
			return nil, err
		}
		if _, ok := seen[input.Name]; ok {
			return nil, validation(path+".name", "duplicate_name")
		}
		seen[input.Name] = struct{}{}
		if len(input.Fields) < 2 || len(input.Fields) > 8 {
			return nil, validation(path+".fields", "invalid_count")
		}
		fieldSeen := map[string]struct{}{}
		for j, name := range input.Fields {
			field, ok := available[name]
			if !ok {
				return nil, validation(fmt.Sprintf("%s.fields.%d", path, j), "unknown_field")
			}
			if field.Type == TypeFile || field.Type == TypeRelation || field.Type == TypeJSONB {
				return nil, validation(fmt.Sprintf("%s.fields.%d", path, j), "unsupported_field")
			}
			if _, ok := fieldSeen[name]; ok {
				return nil, validation(fmt.Sprintf("%s.fields.%d", path, j), "duplicate_name")
			}
			fieldSeen[name] = struct{}{}
		}
		signature := strings.Join(input.Fields, "\x00")
		if _, ok := seenFields[signature]; ok {
			return nil, validation(path+".fields", "duplicate_index")
		}
		seenFields[signature] = struct{}{}
		result = append(result, PlannedIndex{Name: input.Name, Fields: append([]string(nil), input.Fields...), Unique: input.Unique})
	}
	return result, nil
}

func compoundIndexName(collection, name string) string {
	return indexName(collection, "compound:"+name)
}

func createCompoundIndexStatement(collection, table string, index PlannedIndex, fields []PlannedField) string {
	prefix := "CREATE INDEX "
	if index.Unique {
		prefix = "CREATE UNIQUE INDEX "
	}
	physical := make(map[string]string, len(fields))
	for _, field := range fields {
		physical[field.Name] = field.ColumnName
	}
	columns := make([]string, len(index.Fields))
	for i, field := range index.Fields {
		columns[i] = pgx.Identifier{physical[field]}.Sanitize()
	}
	return prefix + pgx.Identifier{compoundIndexName(collection, index.Name)}.Sanitize() + " ON " + pgx.Identifier{"app", table}.Sanitize() + " (" + strings.Join(columns, ", ") + ")"
}

func compoundIndexChangeStatements(collection, table string, current []Index, desired []PlannedIndex, fields []PlannedField) []string {
	old := map[string]Index{}
	next := map[string]PlannedIndex{}
	for _, index := range current {
		old[index.Name] = index
	}
	for _, index := range desired {
		next[index.Name] = index
	}
	statements := []string{}
	for _, index := range current {
		desiredIndex, ok := next[index.Name]
		if !ok || index.Unique != desiredIndex.Unique || !stringSlicesEqual(index.Fields, desiredIndex.Fields) {
			statements = append(statements, "DROP INDEX IF EXISTS "+pgx.Identifier{"app", index.IndexName}.Sanitize())
		}
	}
	for _, index := range desired {
		currentIndex, ok := old[index.Name]
		if !ok || currentIndex.Unique != index.Unique || !stringSlicesEqual(currentIndex.Fields, index.Fields) {
			statements = append(statements, createCompoundIndexStatement(collection, table, index, fields))
		}
	}
	return statements
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
