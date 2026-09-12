package query

import (
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// CompileRecordPredicate compiles a bounded scalar filter against a supplied
// row alias. The returned arguments begin after argOffset.
func CompileRecordPredicate(collection schema.Collection, filterText, alias string, argOffset int) (string, []any, error) {
	if argOffset < 0 || argOffset > 1024 {
		return "", nil, validation("filter", "invalid_filter")
	}
	node, err := parseFilterRequest(filterText)
	if err != nil {
		return "", nil, err
	}
	builder := &statementBuilder{args: make([]any, argOffset)}
	predicate, err := compileAliasedFilter(collection, node, quoted(alias), builder)
	if err != nil {
		return "", nil, err
	}
	return predicate, builder.args[argOffset:], nil
}

func compileAliasedFilter(collection schema.Collection, node filterNode, alias string, builder *statementBuilder) (string, error) {
	if node == nil {
		return "", nil
	}
	switch value := node.(type) {
	case filterAnd:
		return compileAliasedGroup(collection, value.children, " AND ", alias, builder)
	case filterOr:
		return compileAliasedGroup(collection, value.children, " OR ", alias, builder)
	case filterComparison:
		field, ok := filterField(collection, value.field)
		if !ok {
			return "", validation("filter", "unknown_field")
		}
		if field.Type == schema.TypeRelation {
			return "", validation("filter", "relation_filter_unsupported")
		}
		field, identityCollectionID := textIdentityField(collection, field)
		column := alias + "." + quoted(field.ColumnName)
		if identityCollectionID != uuid.Nil {
			column = recordIdentityExpression(identityCollectionID, alias+"."+quoted(systemFieldID))
		}
		if value.otherField != "" {
			other, ok := filterField(collection, value.otherField)
			if !ok {
				return "", validation("filter", "unknown_field")
			}
			other, otherIdentityCollectionID := textIdentityField(collection, other)
			otherColumn := alias + "." + quoted(other.ColumnName)
			if otherIdentityCollectionID != uuid.Nil {
				otherColumn = recordIdentityExpression(otherIdentityCollectionID, alias+"."+quoted(systemFieldID))
			}
			return compileFieldComparison(field, other, value, column, otherColumn)
		}
		return compileQualifiedComparison(field, value, column, builder)
	default:
		return "", validation("filter", "invalid_filter")
	}
}

func compileAliasedGroup(collection schema.Collection, children []filterNode, separator, alias string, builder *statementBuilder) (string, error) {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		part, err := compileAliasedFilter(collection, child, alias, builder)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return "(" + strings.Join(parts, separator) + ")", nil
}
