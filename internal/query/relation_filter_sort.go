package query

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	maximumRelationFilterDepth  = 3
	maximumRelationFilterJoins  = 8
	maximumRelationSortDepth    = 2
	maximumRelationSortRootRows = 100_000
	relationRootAlias           = "rq_root"
)

type relationListPlan struct {
	filter *plannedRelationFilter
	order  orderSpec
}

type plannedRelationFilter struct {
	separator  string
	children   []*plannedRelationFilter
	comparison *plannedRelationComparison
}

type plannedRelationComparison struct {
	input     filterComparison
	rootField *schema.Field
	path      *plannedRelationPath
}

type plannedRelationPath struct {
	hops               []plannedRelationHop
	terminal           schema.Field
	terminalIsRelation bool
}

type plannedRelationHop struct {
	field  schema.Field
	target schema.Collection
}

// compileRelationListPlan resolves every path against one pinned catalog and
// enforces all static budgets before any record query is built or executed.
func compileRelationListPlan(
	catalog schema.Catalog,
	root schema.Collection,
	filter filterNode,
	sortKeys []SortKey,
) (relationListPlan, error) {
	plannedFilter, filterJoins, err := planRelationFilter(catalog, root, filter)
	if err != nil {
		return relationListPlan{}, err
	}
	if filterJoins > maximumRelationFilterJoins {
		return relationListPlan{}, relationFilterUnsupported()
	}

	order, err := planRelationSort(catalog, root, sortKeys)
	if err != nil {
		return relationListPlan{}, err
	}
	return relationListPlan{filter: plannedFilter, order: order}, nil
}

func planRelationFilter(catalog schema.Catalog, root schema.Collection, node filterNode) (*plannedRelationFilter, int, error) {
	if node == nil {
		return nil, 0, nil
	}
	switch value := node.(type) {
	case filterAnd:
		return planRelationFilterGroup(catalog, root, value.children, " AND ")
	case filterOr:
		return planRelationFilterGroup(catalog, root, value.children, " OR ")
	case filterComparison:
		field, ok := filterField(root, value.field)
		if ok && field.Type != schema.TypeRelation && !strings.Contains(value.field, ".") {
			copy := field
			return &plannedRelationFilter{comparison: &plannedRelationComparison{input: value, rootField: &copy}}, 0, nil
		}
		if !ok && !strings.Contains(value.field, ".") {
			return nil, 0, validation("filter", "unknown_field")
		}
		path, err := resolveRelationPath(catalog, root, value.field, "filter")
		if err != nil {
			return nil, 0, err
		}
		if len(path.hops) == 0 || len(path.hops) > maximumRelationFilterDepth {
			return nil, 0, relationFilterUnsupported()
		}
		if path.terminalIsRelation && (value.operator != "=" || value.value.kind != filterLiteralString) {
			return nil, 0, relationFilterUnsupported()
		}
		copy := path
		return &plannedRelationFilter{comparison: &plannedRelationComparison{input: value, path: &copy}}, len(path.hops), nil
	default:
		return nil, 0, validation("filter", "invalid_filter")
	}
}

func planRelationFilterGroup(
	catalog schema.Catalog,
	root schema.Collection,
	children []filterNode,
	separator string,
) (*plannedRelationFilter, int, error) {
	planned := &plannedRelationFilter{separator: separator, children: make([]*plannedRelationFilter, 0, len(children))}
	joins := 0
	for _, child := range children {
		node, childJoins, err := planRelationFilter(catalog, root, child)
		if err != nil {
			return nil, 0, err
		}
		joins += childJoins
		if joins > maximumRelationFilterJoins {
			return nil, 0, relationFilterUnsupported()
		}
		planned.children = append(planned.children, node)
	}
	return planned, joins, nil
}

func planRelationSort(catalog schema.Catalog, root schema.Collection, keys []SortKey) (orderSpec, error) {
	terms := make([]orderTerm, 0, len(keys))
	for _, key := range keys {
		field, ok := filterField(root, key.Field)
		if ok && field.Type != schema.TypeRelation && !strings.Contains(key.Field, ".") {
			if field.Type == schema.TypeJSONB {
				return orderSpec{}, validation("sort", "unsupported_type")
			}
			indexHint, indexed := indexedOrderHint(field, orderIndexRoot)
			if !indexed {
				return orderSpec{}, validation("sort", "unindexed_field")
			}
			terms = append(terms, orderTerm{
				key: key, expression: quoted(field.ColumnName), cursorField: field, indexHint: indexHint,
			})
			continue
		}
		if !ok && !strings.Contains(key.Field, ".") {
			return orderSpec{}, validation("sort", "unknown_field")
		}
		path, err := resolveRelationPath(catalog, root, key.Field, "sort")
		if err != nil {
			return orderSpec{}, err
		}
		indexHint, indexed := indexedOrderHint(path.terminal, orderIndexRelationTarget)
		if len(path.hops) == 0 || len(path.hops) > maximumRelationSortDepth || path.terminal.Type == schema.TypeJSONB || !indexed {
			return orderSpec{}, relationSortUnsupported()
		}
		for _, hop := range path.hops {
			if hop.field.Relation == nil || hop.field.Relation.Cardinality != schema.RelationSingle {
				return orderSpec{}, relationSortUnsupported()
			}
		}
		copy := path
		terms = append(terms, orderTerm{
			key: key, cursorField: path.terminal, relationPath: &copy, indexHint: indexHint,
		})
	}
	return orderSpec{root: root, terms: terms}, nil
}

func resolveRelationPath(catalog schema.Catalog, root schema.Collection, text, operation string) (plannedRelationPath, error) {
	segments := strings.Split(text, ".")
	if len(segments) == 0 {
		return plannedRelationPath{}, relationPathUnsupported(operation)
	}
	current := root
	path := plannedRelationPath{}
	for index, segment := range segments {
		if !validSortFieldSegment(segment) {
			return plannedRelationPath{}, relationPathUnsupported(operation)
		}
		field, ok := filterField(current, segment)
		if !ok {
			return plannedRelationPath{}, relationPathUnsupported(operation)
		}
		if field.Type != schema.TypeRelation {
			if index != len(segments)-1 || len(path.hops) == 0 {
				return plannedRelationPath{}, relationPathUnsupported(operation)
			}
			path.terminal = field
			return path, nil
		}
		if field.Relation == nil {
			return plannedRelationPath{}, schema.ErrConflict
		}
		if _, err := relation.Bind(catalog, current); err != nil {
			return plannedRelationPath{}, schema.ErrConflict
		}
		target, ok := catalogCollectionByID(catalog, field.Relation.TargetCollectionID)
		if !ok {
			return plannedRelationPath{}, schema.ErrConflict
		}
		path.hops = append(path.hops, plannedRelationHop{field: field, target: target})
		current = target
		if index == len(segments)-1 {
			path.terminal = schema.Field{Name: systemFieldID, ColumnName: systemFieldID, Type: schema.TypeUUID, Indexed: true}
			path.terminalIsRelation = true
			return path, nil
		}
	}
	return plannedRelationPath{}, relationPathUnsupported(operation)
}

func validSortFieldSegment(segment string) bool {
	if segment == systemFieldID || segment == systemFieldCreatedAt || segment == systemFieldUpdatedAt {
		return true
	}
	if len(segment) == 0 || len(segment) > 48 {
		return false
	}
	for index := range len(segment) {
		current := segment[index]
		switch {
		case current >= 'a' && current <= 'z':
		case current >= '0' && current <= '9' && index > 0:
		case current == '_':
		default:
			return false
		}
	}
	return true
}

func relationPathUnsupported(operation string) error {
	if operation == "sort" {
		return relationSortUnsupported()
	}
	return relationFilterUnsupported()
}

func relationFilterUnsupported() error { return validation("filter", "relation_filter_unsupported") }
func relationSortUnsupported() error   { return validation("sort", "relation_sort_unsupported") }
func relationSortBudgetExceeded() error {
	return validation("sort", "relation_sort_budget_exceeded")
}

type relationSQLRenderer struct {
	builder *statementBuilder
	next    int
}

func (r *relationSQLRenderer) alias(prefix string) string {
	r.next++
	return prefix + "_" + strconv.Itoa(r.next)
}

func (p relationListPlan) pageStatement(after *PageCursor, fetchLimit int) (Statement, error) {
	builder := &statementBuilder{}
	renderer := &relationSQLRenderer{builder: builder}
	filterSQL, err := compilePlannedRelationFilter(p.filter, renderer, relationRootAlias)
	if err != nil {
		return Statement{}, err
	}
	order, joins, err := p.order.renderRelation(renderer, relationRootAlias, "rq_sort")
	if err != nil {
		return Statement{}, err
	}
	conditions := make([]string, 0, 2)
	if filterSQL != "" {
		conditions = append(conditions, filterSQL)
	}
	if after != nil {
		keyset, err := order.keyset(after, builder)
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, keyset)
	}

	selects := []string{selectedRelationRootColumns(p.order.root, relationRootAlias)}
	selects = order.appendHiddenCursorSelects(selects)
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + quotedTable(p.order.root) + " AS " + quoted(relationRootAlias)
	sql += strings.Join(joins, "")
	if len(conditions) != 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	sql += order.orderBy()
	sql += " LIMIT " + builder.placeholder(fetchLimit)
	return Statement{SQL: sql, Args: builder.args}, nil
}

func (s orderSpec) renderRelation(
	renderer *relationSQLRenderer,
	rootAlias string,
	prefix string,
) (orderSpec, []string, error) {
	terms := append([]orderTerm(nil), s.terms...)
	joins := make([]string, 0, len(terms)*maximumRelationSortDepth)
	for index := range terms {
		term := &terms[index]
		if term.relationPath == nil {
			term.expression = qualified(rootAlias, term.cursorField.ColumnName)
			continue
		}
		expression, pathJoins, err := renderRelationSortJoins(term.relationPath, renderer, rootAlias, prefix)
		if err != nil {
			return orderSpec{}, nil, err
		}
		joins = append(joins, pathJoins...)
		term.expression = expression
	}
	s.terms = terms
	return s, joins, nil
}

func (s orderSpec) anchorStatement(id uuid.UUID) (Statement, error) {
	if id == uuid.Nil {
		return Statement{}, invalidCursor()
	}
	builder := &statementBuilder{}
	renderer := &relationSQLRenderer{builder: builder}
	order, joins, err := s.renderRelation(renderer, relationRootAlias, "rq_anchor")
	if err != nil {
		return Statement{}, err
	}
	selects := order.cursorSelects()
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + quotedTable(s.root) + " AS " + quoted(relationRootAlias) +
		strings.Join(joins, "") + " WHERE " + qualified(relationRootAlias, systemFieldID) + " = " + builder.placeholder(id)
	return Statement{SQL: sql, Args: builder.args}, nil
}

func compilePlannedRelationFilter(node *plannedRelationFilter, renderer *relationSQLRenderer, sourceAlias string) (string, error) {
	if node == nil {
		return "", nil
	}
	if node.comparison != nil {
		comparison := node.comparison
		if comparison.rootField != nil {
			return compileQualifiedComparison(*comparison.rootField, comparison.input, qualified(sourceAlias, comparison.rootField.ColumnName), renderer.builder)
		}
		return renderRelationExists(comparison.path, renderer, sourceAlias, func(alias string) (string, error) {
			return compileQualifiedComparison(comparison.path.terminal, comparison.input, qualified(alias, comparison.path.terminal.ColumnName), renderer.builder)
		})
	}
	parts := make([]string, 0, len(node.children))
	for _, child := range node.children {
		part, err := compilePlannedRelationFilter(child, renderer, sourceAlias)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return "(" + strings.Join(parts, node.separator) + ")", nil
}

func compileQualifiedComparison(field schema.Field, comparison filterComparison, column string, builder *statementBuilder) (string, error) {
	if field.Type == schema.TypeJSONB || field.Type == schema.TypeRelation {
		return "", relationFilterUnsupported()
	}
	if field.Type == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=" {
		return "", validation("filter", "type_mismatch")
	}
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
		value = !value.(bool)
		return "(" + column + " = " + builder.placeholder(value) + ")", nil
	}
	return "(" + column + " " + comparison.operator + " " + builder.placeholder(value) + ")", nil
}

func renderRelationExists(
	path *plannedRelationPath,
	renderer *relationSQLRenderer,
	sourceAlias string,
	terminal func(string) (string, error),
) (string, error) {
	var render func(int, string) (string, error)
	render = func(index int, parentAlias string) (string, error) {
		hop := path.hops[index]
		targetAlias := renderer.alias("rq_filter")
		conditions := make([]string, 0, 3)
		from := quotedTable(hop.target) + " AS " + quoted(targetAlias)
		if hop.field.Relation == nil {
			return "", schema.ErrConflict
		}
		switch hop.field.Relation.Cardinality {
		case schema.RelationSingle:
			conditions = append(conditions, qualified(targetAlias, systemFieldID)+" = "+qualified(parentAlias, hop.field.ColumnName))
		case schema.RelationMultiple:
			edgeAlias := renderer.alias("rq_edge")
			edgeTable := pgx.Identifier{"pgbase_relation", hop.field.Relation.Storage.Name}.Sanitize()
			from = edgeTable + " AS " + quoted(edgeAlias) + " JOIN " + from + " ON " +
				qualified(targetAlias, systemFieldID) + " = " + qualified(edgeAlias, "target_id")
			conditions = append(conditions, qualified(edgeAlias, "source_id")+" = "+qualified(parentAlias, systemFieldID))
		default:
			return "", schema.ErrConflict
		}
		var inner string
		var err error
		if index+1 == len(path.hops) {
			inner, err = terminal(targetAlias)
		} else {
			inner, err = render(index+1, targetAlias)
		}
		if err != nil {
			return "", err
		}
		conditions = append(conditions, inner)
		return "EXISTS (SELECT 1 FROM " + from + " WHERE " + strings.Join(conditions, " AND ") + ")", nil
	}
	if path == nil || len(path.hops) == 0 {
		return "", relationFilterUnsupported()
	}
	return render(0, sourceAlias)
}

func renderRelationSortJoins(
	path *plannedRelationPath,
	renderer *relationSQLRenderer,
	sourceAlias string,
	prefix string,
) (string, []string, error) {
	if path == nil || len(path.hops) == 0 {
		return "", nil, relationSortUnsupported()
	}
	joins := make([]string, 0, len(path.hops))
	parentAlias := sourceAlias
	for index := range path.hops {
		hop := path.hops[index]
		if hop.field.Relation == nil || hop.field.Relation.Cardinality != schema.RelationSingle {
			return "", nil, relationSortUnsupported()
		}
		targetAlias := renderer.alias(prefix)
		conditions := []string{qualified(targetAlias, systemFieldID) + " = " + qualified(parentAlias, hop.field.ColumnName)}
		joins = append(joins, " LEFT JOIN "+quotedTable(hop.target)+" AS "+quoted(targetAlias)+
			" ON "+strings.Join(conditions, " AND "))
		parentAlias = targetAlias
	}
	return qualified(parentAlias, path.terminal.ColumnName), joins, nil
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
