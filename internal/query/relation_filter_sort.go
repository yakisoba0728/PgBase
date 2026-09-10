package query

import (
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
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
	input                         filterComparison
	rootField                     *schema.Field
	otherRootField                *schema.Field
	path                          *plannedRelationPath
	rootIdentityCollectionID      uuid.UUID
	otherRootIdentityCollectionID uuid.UUID
}

type plannedRelationPath struct {
	hops                 []plannedRelationHop
	terminal             schema.Field
	terminalIsRelation   bool
	geoProperty          string
	jsonPath             []string
	identityCollectionID uuid.UUID
}

type plannedRelationHop struct {
	field        schema.Field
	target       schema.Collection
	reverse      bool
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
		if value.function != nil {
			return &plannedRelationFilter{comparison: &plannedRelationComparison{input: value}}, 0, nil
		}
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
			copy, identityCollectionID := textIdentityField(root, field)
			planned := &plannedRelationComparison{input: value, rootField: &copy, rootIdentityCollectionID: identityCollectionID}
			if value.otherField != "" {
				other, found := filterField(root, value.otherField)
				if !found || other.Type == schema.TypeRelation || strings.Contains(value.otherField, ".") {
					return nil, 0, validation("filter", "unknown_field")
				}
				otherCopy, otherIdentityCollectionID := textIdentityField(root, other)
				planned.otherRootField = &otherCopy
				planned.otherRootIdentityCollectionID = otherIdentityCollectionID
			}
			return &plannedRelationFilter{comparison: planned}, 0, nil
		}
		if !ok && !strings.Contains(value.field, ".") {
			return nil, 0, validation("filter", "unknown_field")
		}
		path, err := resolveRelationPath(catalog, root, value.field, "filter")
		if err != nil {
			return nil, 0, err
		}
		if (len(path.hops) == 0 && path.geoProperty == "" && len(path.jsonPath) == 0) || len(path.hops) > maximumRelationFilterDepth {
			return nil, 0, relationFilterUnsupported()
		}
		if path.terminalIsRelation && (strings.TrimPrefix(value.operator, "?") != "=" || value.value.kind != filterLiteralString) {
			return nil, 0, relationFilterUnsupported()
		}
		if value.modifier != "" && !(value.modifier == "each" && len(path.hops) != 0) && path.terminal.Type != schema.TypeText && path.terminal.Type != schema.TypeEditor {
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
		if key.Field == "@random" {
			field, _ := filterField(root, systemFieldID)
			terms = append(terms, orderTerm{key: SortKey{Field: systemFieldID}, expression: "random()", cursorField: field, random: true})
			continue
		}
		if key.Field == externalIDSort {
			field := externalIdentityCursorField(root)
			terms = append(terms, orderTerm{key: SortKey{Field: systemFieldID, Desc: key.Desc}, expression: recordIdentityExpression(root.AccessCollectionID(), qualified(root.TableName, systemFieldID)), cursorField: field, indexHint: orderIndexRoot, identityCollectionID: root.AccessCollectionID()})
			continue
		}
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
		var reverse bool
		if !ok {
			for _, ref := range catalog.Incoming[current.ID] {
				source, found := catalogCollectionByID(catalog, ref.SourceCollectionID)
				if !found {
					return plannedRelationPath{}, schema.ErrConflict
				}
				var sourceField schema.Field
				for _, candidate := range source.Fields {
					if candidate.ID == ref.SourceFieldID {
						sourceField = candidate
						break
					}
				}
				if sourceField.ID != uuid.Nil && segment == source.Name+"_via_"+sourceField.Name {
					field, current, ok, reverse = sourceField, source, true, true
					break
				}
			}
		}
		if !ok {
			return plannedRelationPath{}, relationPathUnsupported(operation)
		}
		if field.Type != schema.TypeRelation {
			if field.Type == schema.TypeJSONB && index < len(segments)-1 {
				path.terminal = schema.Field{ID: field.ID, Name: field.Name, ColumnName: field.ColumnName, Type: schema.TypeJSONB, Indexed: field.Indexed, Hidden: field.Hidden}
				path.jsonPath = append([]string(nil), segments[index+1:]...)
				return path, nil
			}
			if field.Type == schema.TypeGeoPoint && index+1 == len(segments)-1 && (segments[index+1] == "lat" || segments[index+1] == "lon") {
				path.terminal = schema.Field{ID: uuid.NewSHA1(field.ID, []byte("geoPoint:"+segments[index+1])), Name: field.Name + "." + segments[index+1], ColumnName: field.ColumnName, Type: schema.TypeNumeric, Indexed: field.Indexed, Hidden: field.Hidden}
				path.geoProperty = segments[index+1]
				return path, nil
			}
			if index != len(segments)-1 || len(path.hops) == 0 {
				return plannedRelationPath{}, relationPathUnsupported(operation)
			}
			path.terminal, path.identityCollectionID = textIdentityField(current, field)
			if operation == "sort" && path.identityCollectionID != uuid.Nil {
				path.terminal = externalIdentityCursorField(current)
			}
			return path, nil
		}
		if field.Relation == nil {
			return plannedRelationPath{}, schema.ErrConflict
		}
		if _, err := relation.Bind(catalog, current); err != nil {
			return plannedRelationPath{}, schema.ErrConflict
		}
		target := current
		if !reverse {
			target, ok = catalogCollectionByID(catalog, field.Relation.TargetCollectionID)
			if !ok {
				return plannedRelationPath{}, schema.ErrConflict
			}
		}
		path.hops = append(path.hops, plannedRelationHop{field: field, target: target, reverse: reverse})
		current = target
		if index == len(segments)-1 {
			path.terminal = schema.Field{Name: systemFieldID, ColumnName: systemFieldID, Type: schema.TypeUUID, Indexed: true}
			path.terminal, path.identityCollectionID = textIdentityField(current, path.terminal)
			if operation == "sort" && path.identityCollectionID != uuid.Nil {
				path.terminal = externalIdentityCursorField(current)
			}
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
	root    schema.Collection
	catalog schema.Catalog
}

func (r *relationSQLRenderer) alias(prefix string) string {
	r.next++
	return prefix + "_" + strconv.Itoa(r.next)
}

func (p relationListPlan) pageStatementWithPolicy(after *pageBoundary, fetchLimit int, ownerID *uuid.UUID, root ruleBinding, offsets ...int) (Statement, error) {
	builder := &statementBuilder{}
	renderer := &relationSQLRenderer{builder: builder, root: p.order.root, catalog: p.catalog}
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
		predicate, args, err := compilePolicyPredicateWithRequest(*root.rule, p.order.root, root.collection, relationRootAlias, root.principal, len(builder.args), p.catalog, root.request)
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
	if len(offsets) > 0 && offsets[0] > 0 {
		sql += " OFFSET " + builder.placeholder(offsets[0])
	}
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
		if term.random {
			term.expression = "random()"
			continue
		}
		if term.relationPath != nil && term.relationPath.geoProperty != "" && len(term.relationPath.hops) == 0 {
			term.expression = geoPointExpression(qualified(rootAlias, term.cursorField.ColumnName), term.relationPath.geoProperty)
			continue
		}
		if term.relationPath == nil {
			term.expression = qualified(rootAlias, term.cursorField.ColumnName)
			if term.identityCollectionID != uuid.Nil {
				term.expression = recordIdentityExpression(term.identityCollectionID, qualified(rootAlias, systemFieldID))
			}
			continue
		}
		expression, pathJoins, err := renderRelationSortJoins(term.relationPath, renderer, rootAlias, prefix, term.key.Desc)
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
		if comparison.input.function != nil {
			column, field, err := compileFilterFunction(renderer.root, comparison.input.function, renderer.builder)
			if err != nil {
				return "", err
			}
			return compileQualifiedComparison(field, comparison.input, column, renderer.builder)
		}
		if comparison.rootField != nil {
			leftColumn := qualified(sourceAlias, comparison.rootField.ColumnName)
			if comparison.rootIdentityCollectionID != uuid.Nil {
				leftColumn = recordIdentityExpression(comparison.rootIdentityCollectionID, qualified(sourceAlias, systemFieldID))
			}
			if comparison.otherRootField != nil {
				rightColumn := qualified(sourceAlias, comparison.otherRootField.ColumnName)
				if comparison.otherRootIdentityCollectionID != uuid.Nil {
					rightColumn = recordIdentityExpression(comparison.otherRootIdentityCollectionID, qualified(sourceAlias, systemFieldID))
				}
				return compileFieldComparison(*comparison.rootField, *comparison.otherRootField, comparison.input, leftColumn, rightColumn)
			}
			return compileQualifiedComparison(*comparison.rootField, comparison.input, leftColumn, renderer.builder)
		}
		if comparison.path.geoProperty != "" && len(comparison.path.hops) == 0 {
			column := geoPointExpression(qualified(sourceAlias, comparison.path.terminal.ColumnName), comparison.path.geoProperty)
			return compileQualifiedComparison(comparison.path.terminal, comparison.input, column, renderer.builder)
		}
		if len(comparison.path.jsonPath) != 0 && len(comparison.path.hops) == 0 {
			column, field := jsonPathExpression(qualified(sourceAlias, comparison.path.terminal.ColumnName), comparison.path.jsonPath, comparison.input.value)
			return compileQualifiedComparison(field, comparison.input, column, renderer.builder)
		}
		if comparison.input.modifier == "each" && len(comparison.path.hops) != 0 {
			input := comparison.input
			input.modifier = ""
			var ok bool
			input.operator, ok = invertFilterOperator(input.operator)
			if !ok {
				return "", validation("filter", "invalid_filter")
			}
			mismatch, err := renderRelationExists(comparison.path, renderer, sourceAlias, func(alias string) (string, error) {
				column := qualified(alias, comparison.path.terminal.ColumnName)
				if comparison.path.identityCollectionID != uuid.Nil {
					column = recordIdentityExpression(comparison.path.identityCollectionID, qualified(alias, systemFieldID))
				}
				field := comparison.path.terminal
				if comparison.path.geoProperty != "" {
					column = geoPointExpression(column, comparison.path.geoProperty)
				}
				if len(comparison.path.jsonPath) != 0 {
					column, field = jsonPathExpression(column, comparison.path.jsonPath, input.value)
				}
				return compileQualifiedComparison(field, input, column, renderer.builder)
			})
			if err != nil {
				return "", err
			}
			return "NOT (" + mismatch + ")", nil
		}
		return renderRelationExists(comparison.path, renderer, sourceAlias, func(alias string) (string, error) {
			column := qualified(alias, comparison.path.terminal.ColumnName)
			if comparison.path.identityCollectionID != uuid.Nil {
				column = recordIdentityExpression(comparison.path.identityCollectionID, qualified(alias, systemFieldID))
			}
			if comparison.path.geoProperty != "" {
				column = geoPointExpression(column, comparison.path.geoProperty)
			}
			field := comparison.path.terminal
			if len(comparison.path.jsonPath) != 0 {
				column, field = jsonPathExpression(column, comparison.path.jsonPath, comparison.input.value)
			}
			input := comparison.input
			if input.modifier == "each" && comparison.path.terminalIsRelation {
				input.modifier = ""
			}
			return compileQualifiedComparison(field, input, column, renderer.builder)
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

func invertFilterOperator(op string) (string, bool) {
	prefix := ""
	if strings.HasPrefix(op, "?") {
		prefix, op = "?", strings.TrimPrefix(op, "?")
	}
	switch op {
	case "=":
		return prefix + "!=", true
	case "!=":
		return prefix + "=", true
	case "~":
		return prefix + "!~", true
	case "!~":
		return prefix + "~", true
	case "<":
		return prefix + ">=", true
	case "<=":
		return prefix + ">", true
	case ">":
		return prefix + "<=", true
	case ">=":
		return prefix + "<", true
	}
	return "", false
}

func geoPointExpression(column, property string) string {
	return "((" + column + "->>'" + property + "')::numeric)"
}

func jsonPathExpression(column string, path []string, literal filterLiteral) (string, schema.Field) {
	parts := make([]string, len(path))
	for i, part := range path {
		parts[i] = strings.ReplaceAll(part, "'", "''")
	}
	text := "(" + column + "#>>'{" + strings.Join(parts, ",") + "}')"
	field := schema.Field{Type: schema.TypeText}
	switch literal.kind {
	case filterLiteralNumber:
		field.Type = schema.TypeNumeric
		return "(NULLIF(" + text + ",'')::numeric)", field
	case filterLiteralBool:
		field.Type = schema.TypeBoolean
		return "(NULLIF(" + text + ",'')::boolean)", field
	default:
		return text, field
	}
}
func compileQualifiedComparison(field schema.Field, comparison filterComparison, column string, builder *statementBuilder) (string, error) {
	anyMatch := strings.HasPrefix(comparison.operator, "?")
	if anyMatch {
		comparison.operator = strings.TrimPrefix(comparison.operator, "?")
	}
	if field.Type == schema.TypeJSONB || field.Type == schema.TypeGeoPoint || field.Type == schema.TypeFile || field.Type == schema.TypePassword {
		return "", relationFilterUnsupported()
	}
	_ = anyMatch // scalar operands have identical PB any-match behavior
	if (comparison.operator == "~" || comparison.operator == "!~") && (field.Type != schema.TypeText && field.Type != schema.TypeEditor || comparison.value.kind != filterLiteralString) || (field.Type == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=") {
		return "", validation("filter", "type_mismatch")
	}
	if comparison.modifier != "" {
		if field.Type != schema.TypeText && field.Type != schema.TypeEditor {
			return "", validation("filter", "unsupported_modifier")
		}
		switch comparison.modifier {
		case "lower":
			column = "lower(" + column + ")"
		case "length":
			column = "char_length(" + column + ")"
		default:
			return "", validation("filter", "unsupported_modifier")
		}
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
	valueField := field
	if comparison.modifier == "length" {
		valueField.Type = schema.TypeInteger
	}
	value, err := decodeFilterValue(valueField, comparison.value)
	if err != nil {
		return "", err
	}
	if field.Type == schema.TypeBoolean && comparison.operator == "!=" {
		value = !value.(bool)
		return "(" + column + " = " + builder.placeholder(value) + ")", nil
	}
	if comparison.operator == "~" || comparison.operator == "!~" {
		placeholder := builder.placeholder(value)
		if comparison.pocketBaseLike && !comparison.literalLeft {
			pattern := comparison.value.text
			if !strings.Contains(pattern, "%") {
				pattern = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(pattern)
				pattern = "%" + pattern + "%"
			}
			builder.args[len(builder.args)-1] = pattern
			op := "LIKE"
			if comparison.operator == "!~" {
				op = "NOT LIKE"
			}
			return "(" + column + " " + op + " " + placeholder + ` ESCAPE '\')`, nil
		}
		if comparison.literalLeft {
			column, placeholder = placeholder, column
		}
		comparisonSQL := "strpos(" + column + ", " + placeholder + ") > 0"
		if comparison.operator == "!~" {
			comparisonSQL = "NOT (" + comparisonSQL + ")"
		}
		return "(" + comparisonSQL + ")", nil
	}
	return "(" + column + " " + comparison.operator + " " + builder.placeholder(value) + ")", nil
}

func compileFieldComparison(left, right schema.Field, comparison filterComparison, leftColumn, rightColumn string) (string, error) {
	if left.Type != right.Type || left.Type == schema.TypeJSONB || left.Type == schema.TypeGeoPoint || left.Type == schema.TypeFile || left.Type == schema.TypePassword || left.Type == schema.TypeRelation {
		return "", validation("filter", "type_mismatch")
	}
	apply := func(field schema.Field, modifier, column string) (string, error) {
		if modifier == "" {
			return column, nil
		}
		if field.Type != schema.TypeText && field.Type != schema.TypeEditor {
			return "", validation("filter", "unsupported_modifier")
		}
		switch modifier {
		case "lower":
			return "lower(" + column + ")", nil
		case "length":
			return "char_length(" + column + ")", nil
		default:
			return "", validation("filter", "unsupported_modifier")
		}
	}
	var err error
	leftColumn, err = apply(left, comparison.modifier, leftColumn)
	if err != nil {
		return "", err
	}
	rightColumn, err = apply(right, comparison.otherModifier, rightColumn)
	if err != nil {
		return "", err
	}
	if comparison.operator == "~" {
		return "(" + leftColumn + " LIKE ('%' || " + rightColumn + " || '%') ESCAPE '\\')", nil
	}
	if comparison.operator == "!~" {
		return "(" + leftColumn + " NOT LIKE ('%' || " + rightColumn + " || '%') ESCAPE '\\')", nil
	}
	if left.Type == schema.TypeBoolean && comparison.operator != "=" && comparison.operator != "!=" {
		return "", validation("filter", "type_mismatch")
	}
	return "(" + leftColumn + " " + comparison.operator + " " + rightColumn + ")", nil
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
			predicate, args, err := compilePolicyPredicate(*hop.rule, hop.target, hop.policyTarget, targetAlias, hop.principal, len(renderer.builder.args), renderer.catalog)
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
