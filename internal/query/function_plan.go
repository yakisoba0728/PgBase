package query

import (
	"strings"

	"github.com/yakisoba0728/PgBase/internal/schema"
)

type plannedFilterFunction struct {
	name      string
	arguments []plannedFunctionArgument
	result    schema.Field
}

type plannedFunctionArgument struct {
	field    *schema.Field
	property string
	literal  filterLiteral
	typeName schema.FieldType
}

// Bind only the function operands supported by the SQL compiler. Keeping their
// field identities in the plan lets cursors bind the same expression without
// depending on mutable names or generated SQL.
func planFilterFunction(root schema.Collection, comparison filterComparison) (*plannedFilterFunction, error) {
	if comparison.modifier != "" || comparison.otherField != "" || comparison.otherModifier != "" || comparison.literalLeft {
		return nil, validation("filter", "invalid_filter")
	}
	fn := comparison.function
	for index, arg := range fn.args {
		if arg.function != nil || arg.modifier != "" || (fn.name == "strftime" && index == 0 && arg.field != "") {
			return nil, validation("filter", "invalid_filter")
		}
	}
	builder := &statementBuilder{}
	column, result, err := compileFilterFunction(root, fn, builder)
	if err != nil {
		return nil, err
	}
	if _, err := compileQualifiedComparison(result, comparison, column, builder); err != nil {
		return nil, err
	}
	planned := &plannedFilterFunction{name: fn.name, result: result, arguments: make([]plannedFunctionArgument, len(fn.args))}
	for index, arg := range fn.args {
		bound := plannedFunctionArgument{literal: arg.literal, typeName: schema.TypeText}
		if fn.name == "geoDistance" {
			bound.typeName = schema.TypeNumeric
		}
		if arg.field != "" {
			name, property, _ := strings.Cut(arg.field, ".")
			field, ok := filterField(root, name)
			if !ok {
				return nil, validation("filter", "unknown_field")
			}
			bound.field, bound.property, bound.typeName = &field, property, field.Type
		}
		planned.arguments[index] = bound
	}
	return planned, nil
}
