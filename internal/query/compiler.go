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
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
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
		if field.Required && field.Type != schema.TypeFile && len(field.Default) == 0 {
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
	return compileSelectByIDWithMembership(collection, id, nil)
}

func compileSelectByIDWithMembership(collection schema.Collection, id uuid.UUID, membership *int64) Statement {
	statement := Statement{
		SQL:  "SELECT " + selectedColumns(collection) + " FROM " + quotedTable(collection) + " WHERE " + quoted("id") + " = $1",
		Args: []any{id},
	}
	if membership != nil {
		builder := &statementBuilder{args: statement.Args}
		statement.SQL += " AND " + rootMembershipPredicate(collection, collection.TableName, membership, builder)
		statement.Args = builder.args
	}
	return statement
}

func CompileUpdate(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage) (Statement, error) {
	return compileUpdate(collection, id, input, false)
}

// CompileUpdateWithSidecar permits an otherwise empty patch whose sidecar
// participant will mutate transaction-bound metadata before commit.
func CompileUpdateWithSidecar(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage) (Statement, error) {
	return compileUpdate(collection, id, input, true)
}

func compileUpdate(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage, sidecar bool) (Statement, error) {
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

func compileListPage(filter filterNode, order orderSpec, after *pageBoundary, fetchLimit int, ownerID *uuid.UUID, policy ...ruleBinding) (Statement, error) {
	builder := &statementBuilder{}
	filterSQL, err := compileFilter(order.root, filter, builder)
	if err != nil {
		return Statement{}, err
	}
	var conditions []string
	if filterSQL != "" {
		conditions = append(conditions, filterSQL)
	}
	if membership := after.membershipBoundary(); membership != nil {
		conditions = append(conditions, rootMembershipPredicate(order.root, order.root.TableName, membership, builder))
	}
	if after != nil && !after.membershipOnly {
		keysetSQL, err := order.keyset(after, builder)
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, keysetSQL)
	}
	if ownerID != nil {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM pgbase_meta.record_owners AS "rq_owner" WHERE "rq_owner".collection_id = `+builder.placeholder(order.root.AccessCollectionID())+` AND "rq_owner".record_id = `+quoted(order.root.TableName)+`.`+quoted("id")+` AND "rq_owner".user_id = `+builder.placeholder(*ownerID)+`)`)
	}
	if len(policy) > 0 && policy[0].rule != nil {
		predicate, args, err := compilePolicyPredicate(*policy[0].rule, order.root, policy[0].collection, order.root.TableName, policy[0].principal, len(builder.args))
		if err != nil {
			return Statement{}, err
		}
		conditions = append(conditions, "("+predicate+")")
		builder.args = append(builder.args, args...)
	}
	limit := builder.placeholder(fetchLimit)
	statement := "SELECT " + selectedColumns(order.root) + " FROM " + quotedTable(order.root)
	if len(conditions) > 0 {
		statement += " WHERE " + strings.Join(conditions, " AND ")
	}
	statement += order.orderBy() + " LIMIT " + limit
	return Statement{SQL: statement, Args: builder.args}, nil
}

func rootMembershipPredicate(collection schema.Collection, rootAlias string, membership *int64, builder *statementBuilder) string {
	return "EXISTS (SELECT 1 FROM pgbase_meta.record_creation_ordinals AS " + quoted("rq_membership") +
		" WHERE " + qualified("rq_membership", "collection_id") + " = " + builder.placeholder(collection.AccessCollectionID()) +
		" AND " + qualified("rq_membership", "record_id") + " = " + qualified(rootAlias, systemFieldID) +
		" AND " + qualified("rq_membership", "ordinal") + " <= " + builder.placeholder(*membership) + ")"
}

type ruleBinding struct {
	rule       *accessrule.Rule
	principal  identity.Subject
	collection schema.Collection
}

func compilePolicyPredicate(rule accessrule.Rule, visible, policy schema.Collection, alias string, principal identity.Subject, offset int) (string, []any, error) {
	if !visible.IsView() || visible.IsSQLView() {
		return accessrule.Compile(rule, policyCollection(visible), alias, principal, offset, false)
	}
	if policy.ID == uuid.Nil {
		return "", nil, schema.ErrConflict
	}
	policyAlias := alias + "_policy"
	predicate, args, err := accessrule.Compile(rule, policy, policyAlias, principal, offset, false)
	if err != nil {
		return "", nil, err
	}
	return "EXISTS (SELECT 1 FROM " + quotedTable(policy) + " AS " + quoted(policyAlias) + " WHERE " + qualified(policyAlias, systemFieldID) + "=" + qualified(alias, systemFieldID) + " AND (" + predicate + "))", args, nil
}

type statementBuilder struct {
	args []any
}

func (b *statementBuilder) placeholder(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

// rootOrderTerm maps one logical sort key to the ordering term for the root
// table's own column. Every page compiler shares it, so a sort cannot get one
// null placement when a request touches a relation and another when it does
// not -- a cursor issued by either path anchors against the same ordering.
func rootOrderTerm(key SortKey, field schema.Field, allowViewScan ...bool) (orderTerm, error) {
	if field.Type == schema.TypeJSONB || field.Type == schema.TypeGeoPoint || field.Type == schema.TypeFile || field.Type == schema.TypePassword {
		return orderTerm{}, validation("sort", "unsupported_type")
	}
	indexHint, indexed := indexedOrderHint(field, orderIndexRoot)
	if !indexed && (len(allowViewScan) == 0 || !allowViewScan[0]) {
		return orderTerm{}, validation("sort", "unindexed_field")
	}
	term := orderTerm{
		key: key, expression: quoted(field.ColumnName), cursorField: field, indexHint: indexHint,
	}
	if key.Desc && notNullColumn(field) {
		term.nulls = orderNullsFirst
	}
	return term, nil
}

// notNullColumn reports whether the physical column can never hold NULL. The
// planner declares every system column "not null" and a user column follows its
// Required flag, so a descending page over such a column can be ordered with
// PostgreSQL's btree default NULLS FIRST — matching the index without moving a
// single row. Nullable columns keep NULLS LAST, which is the placement the
// relation kernel and the documented API contract also use.
func notNullColumn(field schema.Field) bool {
	switch field.Name {
	case systemFieldID, systemFieldCreatedAt:
		return true
	}
	return field.Required
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
	if field.Type == schema.TypeJSONB || field.Type == schema.TypeGeoPoint || field.Type == schema.TypeFile || field.Type == schema.TypePassword {
		return "", validation("filter", "unsupported_type")
	}
	if isMultipleRelation(field) {
		return "", validation("filter", "relation_filter_unsupported")
	}
	return compileQualifiedComparison(field, comparison, quoted(field.ColumnName), builder)
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
	case schema.TypeText, schema.TypeEditor:
		if literal.kind != filterLiteralString {
			return mismatch()
		}
		return text, nil
	case schema.TypeTimestampTZ, schema.TypeAutoDate:
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
		if field.Type == schema.TypeFile || isMultipleRelation(field) {
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
