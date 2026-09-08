package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func planViewCreate(catalog Catalog, input CreateCollectionInput) (Plan, error) {
	if len(input.Fields) > 0 || len(input.Indexes) > 0 || input.View == nil {
		return Plan{}, validation("view", "invalid_shape")
	}
	source, ok := catalog.Collections[input.View.Source]
	if !ok || source.IsView() {
		return Plan{}, validation("view.source", "unknown_source")
	}
	if len(input.View.Projection) < 1 || len(input.View.Projection) > 128 {
		return Plan{}, validation("view.projection", "invalid_count")
	}
	byName := map[string]Field{}
	for _, field := range source.Fields {
		byName[field.Name] = field
	}
	seen := map[string]bool{}
	fields := make([]PlannedField, 0, len(input.View.Projection))
	for i, name := range input.View.Projection {
		field, ok := byName[name]
		if !ok || seen[name] || field.Type == TypeFile || field.Type == TypeRelation {
			return Plan{}, validation(fmt.Sprintf("view.projection.%d", i), "invalid_field")
		}
		seen[name] = true
		fields = append(fields, PlannedField{Name: name, Type: field.Type, Required: field.Required, Ordinal: i})
	}
	predicate := "TRUE"
	var err error
	if input.View.Filter != nil {
		predicate, err = compileStaticViewFilter(*input.View.Filter, source, "source", 0)
		if err != nil {
			return Plan{}, validation("view.filter", "invalid_filter")
		}
	}
	projection := []string{`"source"."id"`, `"source"."created_at"`, `"source"."updated_at"`}
	for _, name := range input.View.Projection {
		projection = append(projection, `"source".`+pgx.Identifier{byName[name].ColumnName}.Sanitize())
	}
	statement := "CREATE VIEW " + pgx.Identifier{"app", input.Name}.Sanitize() + " AS SELECT " + strings.Join(projection, ", ") + " FROM " + pgx.Identifier{"app", source.TableName}.Sanitize() + ` AS "source" WHERE ` + predicate
	view := &PlannedView{SourceCollectionID: source.ID, Source: source.Name, Projection: append([]string(nil), input.View.Projection...), Filter: cloneViewFilter(input.View.Filter)}
	return Plan{Operation: createCollectionOperation, BaseVersion: catalog.Version, Definition: PlannedCollection{Name: input.Name, TableName: input.Name, Fields: fields, View: view}, Statements: []string{statement}}, nil
}

func compileStaticViewFilter(rule ViewFilter, collection Collection, alias string, depth int) (string, error) {
	if depth > 8 {
		return "", ErrConflict
	}
	switch rule.Op {
	case "all", "any":
		if len(rule.Args) < 1 || len(rule.Args) > 16 || rule.Field != "" || len(rule.Value) > 0 {
			return "", ErrConflict
		}
		parts := make([]string, len(rule.Args))
		for i, child := range rule.Args {
			sql, err := compileStaticViewFilter(child, collection, alias, depth+1)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + sql + ")"
		}
		join := " AND "
		if rule.Op == "any" {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")", nil
	case "eq", "ne", "lt", "lte", "gt", "gte":
		if len(rule.Args) > 0 || rule.Field == "" || len(rule.Value) == 0 {
			return "", ErrConflict
		}
		var field *Field
		for i := range collection.Fields {
			if collection.Fields[i].Name == rule.Field {
				field = &collection.Fields[i]
				break
			}
		}
		if field == nil {
			return "", ErrConflict
		}
		switch field.Type {
		case TypeBoolean, TypeInteger, TypeBigInt, TypeNumeric, TypeText, TypeTimestampTZ, TypeUUID:
		default:
			return "", ErrConflict
		}
		isNull := bytes.Equal(bytes.TrimSpace(rule.Value), []byte("null"))
		if isNull {
			if rule.Op == "eq" {
				return pgx.Identifier{alias}.Sanitize() + "." + pgx.Identifier{field.ColumnName}.Sanitize() + " IS NULL", nil
			}
			if rule.Op == "ne" {
				return pgx.Identifier{alias}.Sanitize() + "." + pgx.Identifier{field.ColumnName}.Sanitize() + " IS NOT NULL", nil
			}
			return "", ErrConflict
		}
		rendered, err := staticViewLiteral(field.Type, rule.Value)
		if err != nil {
			return "", err
		}
		operators := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}
		if (field.Type == TypeBoolean || field.Type == TypeUUID) && (rule.Op != "eq" && rule.Op != "ne") {
			return "", ErrConflict
		}
		return "COALESCE(" + pgx.Identifier{alias}.Sanitize() + "." + pgx.Identifier{field.ColumnName}.Sanitize() + " " + operators[rule.Op] + " " + rendered + ",false)", nil
	}
	return "", ErrConflict
}

func staticViewLiteral(kind FieldType, raw json.RawMessage) (string, error) {
	switch kind {
	case TypeInteger, TypeBigInt:
		var value int64
		if json.Unmarshal(raw, &value) != nil {
			return "", ErrConflict
		}
		if kind == TypeInteger && (value < -2147483648 || value > 2147483647) {
			return "", ErrConflict
		}
		return strconv.FormatInt(value, 10), nil
	case TypeNumeric:
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value json.Number
		if decoder.Decode(&value) != nil || !decimalLiteral.MatchString(value.String()) {
			return "", ErrConflict
		}
		return value.String() + "::numeric", nil
	case TypeTimestampTZ:
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "now" {
			return "", ErrConflict
		}
		_, rendered, err := normalizeDefault(kind, raw)
		return rendered, err
	default:
		_, rendered, err := normalizeDefault(kind, raw)
		return rendered, err
	}
}

func encodeViewFilter(filter *ViewFilter) (any, error) {
	if filter == nil {
		return nil, nil
	}
	raw, err := json.Marshal(filter)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func viewFilterFields(filter *ViewFilter, result map[string]struct{}) {
	if filter == nil {
		return
	}
	if filter.Field != "" {
		result[filter.Field] = struct{}{}
	}
	for i := range filter.Args {
		viewFilterFields(&filter.Args[i], result)
	}
}
