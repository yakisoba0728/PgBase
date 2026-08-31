package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/jsonvalue"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	defaultPageLimit = 50
	maximumPageLimit = 200
)

func CompileInsert(collection schema.Collection, input map[string]json.RawMessage, id uuid.UUID) (Statement, error) {
	decoded, err := decodeInput(collection, input)
	if err != nil {
		return Statement{}, err
	}
	fields := fieldsByOrdinal(collection)
	for _, field := range fields {
		if field.Required && len(field.Default) == 0 {
			if _, present := decoded[field.Name]; !present {
				if field.Type == schema.TypeRelation {
					return Statement{}, validation(field.Name, "relation_min_select")
				}
				return Statement{}, validation(field.Name, "required")
			}
		}
	}

	columns := []string{quoted("id"), quoted("created_at"), quoted("updated_at")}
	args := []any{id}
	values := []string{"$1", "statement_timestamp()", "statement_timestamp()"}
	for _, field := range fields {
		value, present := decoded[field.Name]
		if !present {
			continue
		}
		if isMultipleRelation(field) {
			continue
		}
		columns = append(columns, quoted(field.ColumnName))
		args = append(args, value)
		values = append(values, fmt.Sprintf("$%d", len(args)))
	}
	return Statement{
		SQL: "INSERT INTO " + quotedTable(collection) + " (" + strings.Join(columns, ", ") + ") VALUES (" +
			strings.Join(values, ", ") + ") RETURNING " + selectedColumns(collection),
		Args: args,
	}, nil
}

func CompileSelectByID(collection schema.Collection, id uuid.UUID) Statement {
	return Statement{
		SQL:  "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection) + " WHERE " + quoted("id") + " = $1",
		Args: []any{id},
	}
}

func CompileUpdate(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage) (Statement, error) {
	if len(input) == 0 {
		return Statement{}, validation("", "empty_patch")
	}
	decoded, err := decodeInput(collection, input)
	if err != nil {
		return Statement{}, err
	}
	assignments := make([]string, 0, len(decoded)+1)
	args := make([]any, 0, len(decoded)+2)
	for _, field := range fieldsByOrdinal(collection) {
		value, present := decoded[field.Name]
		if !present {
			continue
		}
		if isMultipleRelation(field) {
			continue
		}
		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s = $%d", quoted(field.ColumnName), len(args)))
	}
	assignments = append(assignments, quoted("updated_at")+" = statement_timestamp()")
	args = append(args, id)
	return Statement{
		SQL: "UPDATE " + quotedTable(collection) + " SET " + strings.Join(assignments, ", ") +
			fmt.Sprintf(" WHERE %s = $%d RETURNING %s", quoted("id"), len(args), selectedColumns(collection)),
		Args: args,
	}, nil
}

func CompileDelete(collection schema.Collection, id uuid.UUID) Statement {
	return Statement{
		SQL:  "DELETE FROM " + quotedTable(collection) + " WHERE " + quoted("id") + " = $1",
		Args: []any{id},
	}
}

func CompileList(collection schema.Collection, query ListQuery) (Statement, error) {
	sortKeys := effectiveSortKeys(query.Sort)
	columns, err := resolveSortColumns(collection, sortKeys)
	if err != nil {
		return Statement{}, err
	}
	builder := &statementBuilder{}
	filterSQL, err := compileFilter(collection, query.Filter, builder)
	if err != nil {
		return Statement{}, err
	}
	var conditions []string
	if filterSQL != "" {
		conditions = append(conditions, filterSQL)
	}
	if query.After != nil {
		keysetSQL, err := compileKeyset(columns, query.After, builder)
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, keysetSQL)
	}
	order := compileOrder(columns)
	limit := builder.placeholder(normalizedFetchLimit(query.Limit))
	statement := "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection)
	if len(conditions) > 0 {
		statement += " WHERE " + strings.Join(conditions, " AND ")
	}
	statement += order + " LIMIT " + limit
	return Statement{SQL: statement, Args: builder.args}, nil
}

// ListQuery carries the parsed list request into compilation. Sort keys are
// logical field names; the compiler resolves them against the catalog.
type ListQuery struct {
	Filter filterNode
	Sort   []SortKey
	After  *PageCursor
	Limit  int
}

type statementBuilder struct {
	args []any
}

func (b *statementBuilder) placeholder(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

type sortColumn struct {
	name  string
	desc  bool
	field schema.Field
}

// resolveSortColumns maps logical sort keys to physical columns. Cursor-driven
// resolution reports failures as invalid cursors instead of input validation.
func resolveSortColumns(collection schema.Collection, keys []SortKey) ([]sortColumn, error) {
	columns := make([]sortColumn, 0, len(keys))
	for _, key := range keys {
		field, ok := filterField(collection, key.Field)
		if !ok || field.Type == schema.TypeJSONB {
			if !ok {
				return nil, validation("sort", "unknown_field")
			}
			return nil, validation("sort", "unsupported_type")
		}
		if isMultipleRelation(field) {
			return nil, validation("sort", "relation_sort_unsupported")
		}
		if !field.Indexed && !field.Unique {
			return nil, validation("sort", "unindexed_field")
		}
		columns = append(columns, sortColumn{name: field.ColumnName, desc: key.Desc, field: field})
	}
	return columns, nil
}

func compileFilter(collection schema.Collection, node filterNode, builder *statementBuilder) (string, error) {
	if node == nil {
		return "", nil
	}
	switch compiled := node.(type) {
	case filterAnd:
		return compileFilterGroup(collection, compiled.children, " AND ", builder)
	case filterOr:
		return compileFilterGroup(collection, compiled.children, " OR ", builder)
	case filterComparison:
		return compileComparison(collection, compiled, builder)
	default:
		return "", validation("filter", "invalid_filter")
	}
}

func compileFilterGroup(collection schema.Collection, children []filterNode, separator string, builder *statementBuilder) (string, error) {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		part, err := compileFilter(collection, child, builder)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return "(" + strings.Join(parts, separator) + ")", nil
}

func compileComparison(collection schema.Collection, comparison filterComparison, builder *statementBuilder) (string, error) {
	field, ok := filterField(collection, comparison.field)
	if !ok {
		return "", validation("filter", "unknown_field")
	}
	if field.Type == schema.TypeJSONB {
		return "", validation("filter", "unsupported_type")
	}
	if isMultipleRelation(field) {
		return "", validation("filter", "relation_filter_unsupported")
	}
	if field.Type == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=" {
		return "", validation("filter", "type_mismatch")
	}
	column := quoted(field.ColumnName)
	if comparison.value.kind == filterLiteralNull {
		switch comparison.operator {
		case "=":
			return "(" + column + " IS NULL)", nil
		case "!=":
			return "(" + column + " IS NOT NULL)", nil
		default:
			return "", validation("filter", "type_mismatch")
		}
	}
	value, err := decodeFilterValue(field, comparison.value)
	if err != nil {
		return "", err
	}
	if field.Type == schema.TypeBoolean && comparison.operator == "!=" {
		// "!= true" is rewritten as "= false" so SQL NULL rows are excluded
		// exactly like a native "column <> value" comparison.
		value = !value.(bool)
		return "(" + column + " = " + builder.placeholder(value) + ")", nil
	}
	return "(" + column + " " + comparison.operator + " " + builder.placeholder(value) + ")", nil
}

func decodeFilterValue(field schema.Field, literal filterLiteral) (any, error) {
	mismatch := func() (any, error) { return nil, validation("filter", "type_mismatch") }
	text := literal.text
	switch field.Type {
	case schema.TypeBoolean:
		if literal.kind != filterLiteralBool {
			return mismatch()
		}
		return literal.bool, nil
	case schema.TypeInteger:
		if literal.kind != filterLiteralNumber {
			return mismatch()
		}
		value, err := strconv.ParseInt(text, 10, 32)
		if err != nil {
			return mismatch()
		}
		return int32(value), nil
	case schema.TypeBigInt:
		if literal.kind == filterLiteralNumber {
			text = literal.text
		} else if literal.kind == filterLiteralString {
			// keep text
		} else {
			return mismatch()
		}
		if !integerStringInput.MatchString(text) {
			return mismatch()
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return mismatch()
		}
		return value, nil
	case schema.TypeNumeric:
		if literal.kind == filterLiteralString {
			// keep text
		} else if literal.kind != filterLiteralNumber {
			return mismatch()
		}
		if !decimalInput.MatchString(text) || !jsonvalue.ValidPostgresNumber(text) {
			return mismatch()
		}
		var numeric pgtype.Numeric
		if err := numeric.Scan(text); err != nil || !numeric.Valid || numeric.NaN || numeric.InfinityModifier != pgtype.Finite {
			return mismatch()
		}
		return numeric, nil
	case schema.TypeText:
		if literal.kind != filterLiteralString {
			return mismatch()
		}
		return text, nil
	case schema.TypeTimestampTZ:
		if literal.kind != filterLiteralString {
			return mismatch()
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || !hasRFC3339Offset(text) {
			return mismatch()
		}
		return parsed.UTC(), nil
	case schema.TypeUUID:
		if literal.kind != filterLiteralString {
			return mismatch()
		}
		parsed, err := uuid.Parse(text)
		if err != nil {
			return mismatch()
		}
		return parsed, nil
	case schema.TypeRelation:
		if field.Relation == nil || field.Relation.Cardinality != schema.RelationSingle || literal.kind != filterLiteralString {
			return mismatch()
		}
		parsed, err := uuid.Parse(text)
		if err != nil || parsed == uuid.Nil {
			return mismatch()
		}
		return parsed, nil
	default:
		return mismatch()
	}
}

// compileKeyset builds the null-safe keyset predicate for the sort order.
// Every sort column is ordered with an explicit NULLS LAST, so a cursor value
// that is NULL has no successors on its own key and a non-NULL value has the
// NULL rows strictly after it in both directions.
func compileKeyset(columns []sortColumn, cursor *PageCursor, builder *statementBuilder) (string, error) {
	if len(cursor.Values) != len(columns) {
		return "", invalidCursor()
	}
	placeholders := make([]string, len(columns))
	for index, raw := range cursor.Values {
		value, err := decodeCursorValue(columns[index].field, raw)
		if err != nil {
			return "", err
		}
		placeholders[index] = builder.placeholder(value)
	}
	disjunctions := make([]string, 0, len(columns))
	for index := range columns {
		conjuncts := make([]string, 0, index+1)
		for prior := 0; prior < index; prior++ {
			conjuncts = append(conjuncts, "("+quoted(columns[prior].name)+" IS NOT DISTINCT FROM "+placeholders[prior]+")")
		}
		operator := ">"
		if columns[index].desc {
			operator = "<"
		}
		column := quoted(columns[index].name)
		conjuncts = append(conjuncts, "("+column+" "+operator+" "+placeholders[index]+" OR ("+placeholders[index]+" IS NOT NULL AND "+column+" IS NULL))")
		conjunction := strings.Join(conjuncts, " AND ")
		if len(conjuncts) > 1 {
			conjunction = "(" + conjunction + ")"
		}
		disjunctions = append(disjunctions, conjunction)
	}
	return "(" + strings.Join(disjunctions, " OR ") + ")", nil
}

// decodeCursorValue decodes a signed cursor position against the sort column's
// declared type. Values that no longer decode — for example after a schema
// change — invalidate the whole cursor.
func decodeCursorValue(field schema.Field, raw json.RawMessage) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, err := decodeFieldValue(field, raw)
	if err != nil {
		return nil, invalidCursor()
	}
	return value, nil
}

func compileOrder(columns []sortColumn) string {
	segments := make([]string, 0, len(columns))
	for _, column := range columns {
		direction := "ASC"
		if column.desc {
			direction = "DESC"
		}
		segments = append(segments, quoted(column.name)+" "+direction+" NULLS LAST")
	}
	return " ORDER BY " + strings.Join(segments, ", ")
}

func normalizedFetchLimit(limit int) int {
	if limit <= 0 {
		limit = defaultPageLimit
	} else if limit > maximumPageLimit {
		limit = maximumPageLimit
	}
	return limit + 1
}

func selectedColumns(collection schema.Collection) string {
	columns := []string{quoted("id"), quoted("created_at"), quoted("updated_at")}
	for _, field := range fieldsByOrdinal(collection) {
		if isMultipleRelation(field) {
			continue
		}
		column := quoted(field.ColumnName)
		if field.Type == schema.TypeNumeric {
			// pgx's binary Numeric decoder drops PostgreSQL's display scale for
			// zero. Text preserves the exact database representation promised by
			// the API's lossless numeric string contract.
			column += "::text"
		}
		columns = append(columns, column)
	}
	return strings.Join(columns, ", ")
}

func quotedTable(collection schema.Collection) string {
	return pgx.Identifier{"app", collection.TableName}.Sanitize()
}

func quoted(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
