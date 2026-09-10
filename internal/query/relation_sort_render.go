package query

import (
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func renderRelationSortJoins(
	path *plannedRelationPath,
	renderer *relationSQLRenderer,
	sourceAlias string,
	prefix string,
	desc bool,
) (string, []string, error) {
	if path == nil || len(path.hops) == 0 {
		return "", nil, relationSortUnsupported()
	}
	joins := make([]string, 0, len(path.hops))
	parentAlias := sourceAlias
	for index := range path.hops {
		hop := path.hops[index]
		if hop.field.Relation == nil {
			return "", nil, relationSortUnsupported()
		}
		targetAlias := renderer.alias(prefix)
		if hop.reverse || hop.field.Relation.Cardinality == schema.RelationMultiple {
			if index != len(path.hops)-1 {
				return "", nil, relationSortUnsupported()
			}
			edgeAlias := renderer.alias(prefix + "_edge")
			lateralAlias := renderer.alias(prefix + "_many")
			conditions := []string{}
			if hop.rule != nil {
				predicate, args, err := compilePolicyPredicate(*hop.rule, hop.target, hop.policyTarget, targetAlias, hop.principal, len(renderer.builder.args), renderer.catalog)
				if err != nil {
					return "", nil, err
				}
				renderer.builder.args = append(renderer.builder.args, args...)
				conditions = append(conditions, predicate)
			}
			direction := " ASC"
			if desc {
				direction = " DESC"
			}
			value := qualified(targetAlias, path.terminal.ColumnName)
			from := quotedTable(hop.target) + " AS " + quoted(targetAlias)
			if hop.reverse && hop.field.Relation.Cardinality == schema.RelationSingle {
				conditions = append(conditions, qualified(targetAlias, hop.field.ColumnName)+" = "+qualified(parentAlias, systemFieldID))
			} else {
				edgeTarget := "target_id"
				if hop.reverse {
					edgeTarget = "source_id"
				}
				from = pgx.Identifier{"pgbase_relation", hop.field.Relation.Storage.Name}.Sanitize() + " AS " + quoted(edgeAlias) + " JOIN " + from + " ON " + qualified(targetAlias, systemFieldID) + " = " + qualified(edgeAlias, edgeTarget)
				if hop.reverse {
					conditions = append(conditions, qualified(edgeAlias, "target_id")+" = "+qualified(parentAlias, systemFieldID))
				} else {
					conditions = append(conditions, qualified(edgeAlias, "source_id")+" = "+qualified(parentAlias, systemFieldID))
				}
			}
			joins = append(joins, " LEFT JOIN LATERAL (SELECT "+value+" AS "+quoted("sort_value")+" FROM "+from+" WHERE "+strings.Join(conditions, " AND ")+" ORDER BY "+value+direction+" NULLS LAST LIMIT 1) AS "+quoted(lateralAlias)+" ON TRUE")
			return qualified(lateralAlias, "sort_value"), joins, nil
		}
		if hop.field.Relation.Cardinality != schema.RelationSingle {
			return "", nil, relationSortUnsupported()
		}
		conditions := []string{qualified(targetAlias, systemFieldID) + " = " + qualified(parentAlias, hop.field.ColumnName)}
		if hop.rule != nil {
			predicate, args, err := compilePolicyPredicate(*hop.rule, hop.target, hop.policyTarget, targetAlias, hop.principal, len(renderer.builder.args), renderer.catalog)
			if err != nil {
				return "", nil, err
			}
			renderer.builder.args = append(renderer.builder.args, args...)
			conditions = append(conditions, predicate)
		}
		joins = append(joins, " LEFT JOIN "+quotedTable(hop.target)+" AS "+quoted(targetAlias)+
			" ON "+strings.Join(conditions, " AND "))
		parentAlias = targetAlias
	}
	expression := qualified(parentAlias, path.terminal.ColumnName)
	if path.geoProperty != "" {
		expression = geoPointExpression(expression, path.geoProperty)
	}
	return expression, joins, nil
}

func selectedRelationRootColumns(collection schema.Collection, alias string) string {
	columns := []string{
		qualified(alias, systemFieldID),
		qualified(alias, systemFieldCreatedAt),
		qualified(alias, systemFieldUpdatedAt),
	}
	for _, field := range fieldsByOrdinal(collection) {
		if isMultipleRelation(field) {
			continue
		}
		column := qualified(alias, field.ColumnName)
		if field.Type == schema.TypeNumeric {
			column += "::text"
		}
		columns = append(columns, column)
	}
	return strings.Join(columns, ", ")
}

func qualified(alias, column string) string { return quoted(alias) + "." + quoted(column) }
