package query

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	ReadActionRelationFilter ReadAction = "relation_filter"
	ReadActionRelationSort   ReadAction = "relation_sort"

	maximumRelationFilterDepth  = 3
	maximumRelationFilterJoins  = 8
	maximumRelationSortDepth    = 2
	maximumRelationSortRootRows = 100_000
	relationRootAlias           = "rq_root"
)

// RelationListRequest is the narrow authenticated seam for relation-aware
// list queries. List keeps its positional compatibility contract; callers
// which can supply an authenticated principal opt into this request instead.
type RelationListRequest struct {
	Principal  Principal
	Collection string
	Cursor     string
	Limit      int
	Filter     string
	Sort       string
}

type relationListPlan struct {
	root          schema.Collection
	filter        *plannedRelationFilter
	sort          []plannedRelationSort
	privateCursor bool
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

type plannedRelationSort struct {
	key       SortKey
	rootField *schema.Field
	path      *plannedRelationPath
	field     schema.Field
}

type plannedRelationPath struct {
	hops               []plannedRelationHop
	terminal           schema.Field
	terminalIsRelation bool
}

type plannedRelationHop struct {
	field     schema.Field
	target    schema.Collection
	predicate Predicate
}

// compileRelationListPlan resolves every path against one pinned catalog,
// enforces all static budgets, and asks the typed policy for every hop before
// any record query is built or executed.
func compileRelationListPlan(
	ctx context.Context,
	catalog schema.Catalog,
	root schema.Collection,
	principal Principal,
	policy ProjectionPolicy,
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

	plannedSort, err := planRelationSort(catalog, root, sortKeys)
	if err != nil {
		return relationListPlan{}, err
	}
	// Authorization deliberately follows structural validation and budgets so
	// an oversized request cannot turn policy calls into unbounded pre-query I/O.
	if err := authorizePlannedFilter(ctx, principal, policy, plannedFilter); err != nil {
		return relationListPlan{}, err
	}
	privateCursor := false
	for index := range plannedSort {
		if plannedSort[index].path == nil {
			continue
		}
		privateCursor = true
		if err := authorizeRelationPath(ctx, principal, policy, plannedSort[index].path, ReadActionRelationSort); err != nil {
			return relationListPlan{}, err
		}
	}
	return relationListPlan{root: root, filter: plannedFilter, sort: plannedSort, privateCursor: privateCursor}, nil
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

func planRelationSort(catalog schema.Catalog, root schema.Collection, keys []SortKey) ([]plannedRelationSort, error) {
	planned := make([]plannedRelationSort, 0, len(keys))
	for _, key := range keys {
		field, ok := filterField(root, key.Field)
		if ok && field.Type != schema.TypeRelation && !strings.Contains(key.Field, ".") {
			if field.Type == schema.TypeJSONB {
				return nil, validation("sort", "unsupported_type")
			}
			if !field.Indexed && !field.Unique {
				return nil, validation("sort", "unindexed_field")
			}
			copy := field
			planned = append(planned, plannedRelationSort{key: key, rootField: &copy, field: field})
			continue
		}
		if !ok && !strings.Contains(key.Field, ".") {
			return nil, validation("sort", "unknown_field")
		}
		path, err := resolveRelationPath(catalog, root, key.Field, "sort")
		if err != nil {
			return nil, err
		}
		if len(path.hops) == 0 || len(path.hops) > maximumRelationSortDepth || path.terminal.Type == schema.TypeJSONB ||
			(!path.terminal.Indexed && !path.terminal.Unique) {
			return nil, relationSortUnsupported()
		}
		for _, hop := range path.hops {
			if hop.field.Relation == nil || hop.field.Relation.Cardinality != schema.RelationSingle {
				return nil, relationSortUnsupported()
			}
		}
		copy := path
		planned = append(planned, plannedRelationSort{key: key, path: &copy, field: path.terminal})
	}
	return planned, nil
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

func authorizePlannedFilter(ctx context.Context, principal Principal, policy ProjectionPolicy, node *plannedRelationFilter) error {
	if node == nil {
		return nil
	}
	if node.comparison != nil && node.comparison.path != nil {
		return authorizeRelationPath(ctx, principal, policy, node.comparison.path, ReadActionRelationFilter)
	}
	for _, child := range node.children {
		if err := authorizePlannedFilter(ctx, principal, policy, child); err != nil {
			return err
		}
	}
	return nil
}

func authorizeRelationPath(
	ctx context.Context,
	principal Principal,
	policy ProjectionPolicy,
	path *plannedRelationPath,
	action ReadAction,
) error {
	for index := range path.hops {
		hop := &path.hops[index]
		predicate, fields, err := relationReadScope(ctx, policy, principal, hop.target, action)
		if err != nil {
			return err
		}
		if err := validateProjection(hop.target, predicate, fields); err != nil {
			return err
		}
		required := path.terminal.Name
		if index+1 < len(path.hops) {
			required = path.hops[index+1].field.Name
		}
		if !projectionAllowsField(fields, required) {
			return ErrProjectionDenied
		}
		hop.predicate = predicate
	}
	return nil
}

func relationReadScope(
	ctx context.Context,
	policy ProjectionPolicy,
	principal Principal,
	collection schema.Collection,
	action ReadAction,
) (Predicate, FieldSet, error) {
	if policy == nil {
		return Predicate{}, FieldSet{}, ErrProjectionDenied
	}
	// AdminAllowAll predates the two typed relation actions. Preserve that
	// adapter's fail-closed authentication contract while custom policies see
	// the precise action for every hop.
	switch policy.(type) {
	case AdminAllowAll, *AdminAllowAll:
		return policy.ReadScope(ctx, principal, collection, ReadActionExpand)
	default:
		return policy.ReadScope(ctx, principal, collection, action)
	}
}

func projectionAllowsField(fields FieldSet, name string) bool {
	if name == systemFieldID || name == systemFieldCreatedAt || name == systemFieldUpdatedAt {
		return true
	}
	if fields.all {
		return true
	}
	_, ok := fields.fields[name]
	return ok
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
	return prefix + "_" + strconvItoa(r.next)
}

// strconvItoa is kept local to avoid exposing aliases to caller input.
func strconvItoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[position:])
}

func (p relationListPlan) statement(after *PageCursor, limit int) (Statement, error) {
	builder := &statementBuilder{}
	renderer := &relationSQLRenderer{builder: builder}
	filterSQL, err := compilePlannedRelationFilter(p.filter, renderer, relationRootAlias)
	if err != nil {
		return Statement{}, err
	}
	columns, joins, err := p.renderSortColumns(renderer, relationRootAlias, "rq_sort")
	if err != nil {
		return Statement{}, err
	}
	conditions := make([]string, 0, 2)
	if filterSQL != "" {
		conditions = append(conditions, filterSQL)
	}
	if after != nil {
		keyset, err := compileRelationKeyset(columns, after, builder)
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, keyset)
	}

	selects := []string{selectedRelationRootColumns(p.root, relationRootAlias)}
	for _, column := range columns {
		if column.relation {
			selects = append(selects, column.cursorExpression)
		}
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + quotedTable(p.root) + " AS " + quoted(relationRootAlias)
	sql += strings.Join(joins, "")
	if len(conditions) != 0 {
		sql += " WHERE " + strings.Join(conditions, " AND ")
	}
	sql += compileRelationOrder(columns)
	sql += " LIMIT " + builder.placeholder(normalizedFetchLimit(limit))
	return Statement{SQL: sql, Args: builder.args}, nil
}

type compiledRelationSort struct {
	expression       string
	cursorExpression string
	desc             bool
	field            schema.Field
	relation         bool
}

func (p relationListPlan) renderSortColumns(
	renderer *relationSQLRenderer,
	rootAlias string,
	prefix string,
) ([]compiledRelationSort, []string, error) {
	columns := make([]compiledRelationSort, 0, len(p.sort))
	joins := make([]string, 0, len(p.sort)*maximumRelationSortDepth)
	for index := range p.sort {
		planned := &p.sort[index]
		if planned.rootField != nil {
			expression := qualified(rootAlias, planned.rootField.ColumnName)
			columns = append(columns, compiledRelationSort{
				expression:       expression,
				cursorExpression: relationCursorExpression(expression, *planned.rootField),
				desc:             planned.key.Desc,
				field:            *planned.rootField,
			})
			continue
		}
		expression, pathJoins, err := renderRelationSortJoins(planned.path, renderer, rootAlias, prefix)
		if err != nil {
			return nil, nil, err
		}
		joins = append(joins, pathJoins...)
		columns = append(columns, compiledRelationSort{
			expression:       expression,
			cursorExpression: relationCursorExpression(expression, planned.field),
			desc:             planned.key.Desc,
			field:            planned.field,
			relation:         true,
		})
	}
	return columns, joins, nil
}

func (p relationListPlan) anchorStatement(id uuid.UUID) (Statement, error) {
	if id == uuid.Nil {
		return Statement{}, invalidCursor()
	}
	builder := &statementBuilder{}
	renderer := &relationSQLRenderer{builder: builder}
	columns, joins, err := p.renderSortColumns(renderer, relationRootAlias, "rq_anchor")
	if err != nil {
		return Statement{}, err
	}
	selects := make([]string, 0, len(columns))
	for _, column := range columns {
		selects = append(selects, column.cursorExpression)
	}
	sql := "SELECT " + strings.Join(selects, ", ") + " FROM " + quotedTable(p.root) + " AS " + quoted(relationRootAlias) +
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
		if policySQL := compileRelationPredicate(hop.predicate, targetAlias, renderer.builder); policySQL != "" {
			conditions = append(conditions, policySQL)
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
		if policySQL := compileRelationPredicate(hop.predicate, targetAlias, renderer.builder); policySQL != "" {
			conditions = append(conditions, policySQL)
		}
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

func relationCursorExpression(expression string, field schema.Field) string {
	if field.Type == schema.TypeNumeric {
		return expression + "::text"
	}
	return expression
}

func compileRelationPredicate(predicate Predicate, alias string, builder *statementBuilder) string {
	switch predicate.kind {
	case predicateAllowAll:
		return ""
	case predicateDenyAll:
		return "FALSE"
	case predicateRecordIDs:
		return qualified(alias, systemFieldID) + " = ANY(" + builder.placeholder(append([]uuid.UUID(nil), predicate.ids...)) + "::uuid[])"
	default:
		return "FALSE"
	}
}

func compileRelationKeyset(columns []compiledRelationSort, cursor *PageCursor, builder *statementBuilder) (string, error) {
	if cursor == nil || len(cursor.Values) != len(columns) {
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
			conjuncts = append(conjuncts, "("+columns[prior].expression+" IS NOT DISTINCT FROM "+placeholders[prior]+")")
		}
		operator := ">"
		if columns[index].desc {
			operator = "<"
		}
		expression := columns[index].expression
		conjuncts = append(conjuncts, "("+expression+" "+operator+" "+placeholders[index]+" OR ("+
			placeholders[index]+" IS NOT NULL AND "+expression+" IS NULL))")
		conjunction := strings.Join(conjuncts, " AND ")
		if len(conjuncts) > 1 {
			conjunction = "(" + conjunction + ")"
		}
		disjunctions = append(disjunctions, conjunction)
	}
	return "(" + strings.Join(disjunctions, " OR ") + ")", nil
}

func compileRelationOrder(columns []compiledRelationSort) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		direction := "ASC"
		if column.desc {
			direction = "DESC"
		}
		parts = append(parts, column.expression+" "+direction+" NULLS LAST")
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

func qualified(alias, column string) string { return quoted(alias) + "." + quoted(column) }

func relationPlanningErrorForCursor(err error, hasCursor bool) error {
	if err == nil || !hasCursor || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return invalidCursor()
}
