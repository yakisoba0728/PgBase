package query

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/accessrule"
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
	filter  *plannedRelationFilter
	order   orderSpec
	catalog schema.Catalog
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
	geoProperty        string
}

type plannedRelationHop struct {
	field        schema.Field
	target       schema.Collection
	policyTarget schema.Collection
	rule         *accessrule.Rule
	ruleRevision int64
	principal    Principal
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
	return relationListPlan{filter: plannedFilter, order: order, catalog: catalog}, nil
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
			// Reject here rather than only at statement compilation: the cursor
			// v5 query fingerprint also walks the planned filter, and a field
			// this plan cannot compile must not reach two decoders that
			// disagree about which validation detail the caller gets.
			if field.Type == schema.TypeJSONB || field.Type == schema.TypeGeoPoint || field.Type == schema.TypeFile || field.Type == schema.TypePassword {
				return nil, 0, validation("filter", "unsupported_type")
			}
			if value.modifier != "" && field.Type != schema.TypeText && field.Type != schema.TypeEditor {
				return nil, 0, validation("filter", "unsupported_modifier")
			}
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
		if (len(path.hops) == 0 && path.geoProperty == "") || len(path.hops) > maximumRelationFilterDepth {
			return nil, 0, relationFilterUnsupported()
		}
		if path.terminalIsRelation && (value.operator != "=" || value.value.kind != filterLiteralString) {
			return nil, 0, relationFilterUnsupported()
		}
		if value.modifier != "" && path.terminal.Type != schema.TypeText && path.terminal.Type != schema.TypeEditor {
			return nil, 0, validation("filter", "unsupported_modifier")
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
		inline, isRoot := rootSortField(field)
		if ok && isRoot && !strings.Contains(key.Field, ".") {
			term, err := rootOrderTerm(key, inline, root.IsSQLView())
			if err != nil {
				return orderSpec{}, err
			}
			terms = append(terms, term)
			continue
		}
		if ok && isMultipleRelation(field) && !strings.Contains(key.Field, ".") {
			// Cardinality alone decides this, and the target is never read, so
			// the rejection must not depend on resolving it.
			return orderSpec{}, relationSortUnsupported()
		}
		if !ok && !strings.Contains(key.Field, ".") {
			return orderSpec{}, validation("sort", "unknown_field")
		}
		path, err := resolveRelationPath(catalog, root, key.Field, "sort")
		if err != nil {
			return orderSpec{}, err
		}
		indexHint, indexed := indexedOrderHint(path.terminal, orderIndexRelationTarget)
		if (len(path.hops) == 0 && path.geoProperty == "") || len(path.hops) > maximumRelationSortDepth || path.terminal.Type == schema.TypeJSONB || path.terminal.Type == schema.TypePassword || !indexed {
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

// rootSortField reports whether a dot-free sort key orders on the root table
// alone, and returns the field as that ordering has to describe it. A single
// relation column holds its target's id under a deferred foreign key and always
// carries its own index, so the LEFT JOIN that ordering by target.id used to
// need is redundant -- the root column is the same value, NULL for NULL. The
// term therefore has to describe the uuid the column physically is, so the
// cursor decodes a uuid and the v5 descriptor encodes "uuid" rather than
// "relation". A multi relation and a relation missing its catalog binding are
// not root work; the path resolver keeps owning their errors.
func rootSortField(field schema.Field) (schema.Field, bool) {
	if field.Type != schema.TypeRelation {
		return field, true
	}
	if field.Relation == nil || field.Relation.Cardinality != schema.RelationSingle {
		return field, false
	}
	field.Type = schema.TypeUUID
	return field, true
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
			if field.Type == schema.TypeGeoPoint && index+1 == len(segments)-1 && (segments[index+1] == "lat" || segments[index+1] == "lon") {
				path.terminal = schema.Field{ID: uuid.NewSHA1(field.ID, []byte("geoPoint:"+segments[index+1])), Name: field.Name + "." + segments[index+1], ColumnName: field.ColumnName, Type: schema.TypeNumeric, Indexed: field.Indexed, Hidden: field.Hidden}
				path.geoProperty = segments[index+1]
				return path, nil
			}
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

func (p relationListPlan) pageStatementWithPolicy(after *pageBoundary, fetchLimit int, ownerID *uuid.UUID, root ruleBinding) (Statement, error) {
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
	if ownerID != nil {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM pgbase_meta.record_owners AS "rq_owner" WHERE "rq_owner".collection_id = `+builder.placeholder(p.order.root.AccessCollectionID())+` AND "rq_owner".record_id = `+qualified(relationRootAlias, "id")+` AND "rq_owner".user_id = `+builder.placeholder(*ownerID)+`)`)
	} else if root.rule != nil {
		predicate, args, err := compilePolicyPredicate(*root.rule, p.order.root, root.collection, relationRootAlias, root.principal, len(builder.args))
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, predicate)
		builder.args = append(builder.args, args...)
	}
	if membership := after.membershipBoundary(); membership != nil {
		conditions = append(conditions, rootMembershipPredicate(p.order.root, relationRootAlias, membership, builder))
	}
	if after != nil && !after.membershipOnly {
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
		if term.relationPath != nil && term.relationPath.geoProperty != "" && len(term.relationPath.hops) == 0 {
			term.expression = geoPointExpression(qualified(rootAlias, term.cursorField.ColumnName), term.relationPath.geoProperty)
			continue
		}
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

func (s orderSpec) anchorStatement(id uuid.UUID, membership *int64) (Statement, error) {
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
	conditions := []string{qualified(relationRootAlias, systemFieldID) + " = " + builder.placeholder(id)}
	if membership != nil {
		conditions = append(conditions, rootMembershipPredicate(s.root, relationRootAlias, membership, builder))
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + quotedTable(s.root) + " AS " + quoted(relationRootAlias) +
		strings.Join(joins, "") + " WHERE " + strings.Join(conditions, " AND ")
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
		if comparison.path.geoProperty != "" && len(comparison.path.hops) == 0 {
			column := geoPointExpression(qualified(sourceAlias, comparison.path.terminal.ColumnName), comparison.path.geoProperty)
			return compileQualifiedComparison(comparison.path.terminal, comparison.input, column, renderer.builder)
		}
		return renderRelationExists(comparison.path, renderer, sourceAlias, func(alias string) (string, error) {
			column := qualified(alias, comparison.path.terminal.ColumnName)
			if comparison.path.geoProperty != "" {
				column = geoPointExpression(column, comparison.path.geoProperty)
			}
			return compileQualifiedComparison(comparison.path.terminal, comparison.input, column, renderer.builder)
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

func geoPointExpression(column, property string) string {
	return "((" + column + "->>'" + property + "')::numeric)"
}

func compileQualifiedComparison(field schema.Field, comparison filterComparison, column string, builder *statementBuilder) (string, error) {
	if field.Type == schema.TypeJSONB || field.Type == schema.TypeGeoPoint || field.Type == schema.TypeFile || field.Type == schema.TypePassword {
		return "", relationFilterUnsupported()
	}
	if field.Type == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=" {
		return "", validation("filter", "type_mismatch")
	}
	if comparison.modifier != "" {
		if comparison.modifier != "lower" || field.Type != schema.TypeText && field.Type != schema.TypeEditor {
			return "", validation("filter", "unsupported_modifier")
		}
		column = "lower(" + column + ")"
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
		if hop.rule != nil {
			predicate, args, err := compilePolicyPredicate(*hop.rule, hop.target, hop.policyTarget, targetAlias, hop.principal, len(renderer.builder.args))
			if err != nil {
				return "", err
			}
			renderer.builder.args = append(renderer.builder.args, args...)
			conditions = append(conditions, predicate)
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
		if hop.rule != nil {
			predicate, args, err := compilePolicyPredicate(*hop.rule, hop.target, hop.policyTarget, targetAlias, hop.principal, len(renderer.builder.args))
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
