package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type reverseExpandPair struct {
	parentID uuid.UUID
	sourceID uuid.UUID
}

func (s *Service) executeReverseExpandNode(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	job expandLevelJob,
	principal Principal,
	ruleContext *accessrule.RequestContext,
	usage *expansionUsage,
) ([]*materializedRecord, error) {
	if len(job.parents) == 0 {
		return nil, nil
	}
	for _, parent := range job.parents {
		parent.attach(job.node.name, materializedExpandValue{many: []*materializedRecord{}, plural: true})
	}
	parentsByID := make(map[uuid.UUID][]*materializedRecord, len(job.parents))
	parentIDs := make([]uuid.UUID, 0, len(job.parents))
	for _, parent := range job.parents {
		id, err := canonicalRecordID(parent.root)
		if err != nil {
			return nil, schema.ErrConflict
		}
		if _, found := parentsByID[id]; !found {
			parentIDs = append(parentIDs, id)
		}
		parentsByID[id] = append(parentsByID[id], parent)
	}
	parentIDs = uniqueSortedIDs(parentIDs)
	remainingRows := usage.limits.maxRows - usage.rows
	if remainingRows < 0 {
		return nil, expandBudgetError()
	}
	discoveryLimit := usage.limits.maxReverseRows
	if remainingRows < discoveryLimit {
		discoveryLimit = remainingRows
	}
	if err := usage.takeQuerySteps(1); err != nil {
		return nil, err
	}
	pairs, err := discoverReverseExpand(ctx, tx, catalog, *job.node, parentIDs, discoveryLimit, principal, ruleContext)
	if err != nil {
		return nil, err
	}
	// The 1,000 cap applies to this whole breadth-first node job across every
	// parent in the batch, not independently to each parent record.
	if len(pairs) > usage.limits.maxReverseRows || len(pairs) > remainingRows {
		return nil, expandBudgetError()
	}
	if err := usage.takeRows(len(pairs)); err != nil {
		return nil, err
	}
	sourceIDs := make([]uuid.UUID, 0, len(pairs))
	for _, pair := range pairs {
		sourceIDs = append(sourceIDs, pair.sourceID)
	}
	sourceIDs = uniqueSortedIDs(sourceIDs)
	if err := usage.takeRows(len(sourceIDs)); err != nil {
		return nil, err
	}
	records, err := s.fetchExpansionTargets(ctx, tx, catalog, job.node.target, sourceIDs, principal, ruleContext, usage)
	if err != nil {
		return nil, err
	}
	targets := make(map[uuid.UUID]*materializedRecord, len(records))
	for id, record := range records {
		targets[id] = &materializedRecord{root: record}
	}

	grouped := make(map[uuid.UUID][]*materializedRecord, len(parentIDs))
	for _, pair := range pairs {
		target, ok := targets[pair.sourceID]
		if !ok {
			return nil, schema.ErrConflict
		}
		grouped[pair.parentID] = append(grouped[pair.parentID], target)
	}
	for parentID, occurrences := range parentsByID {
		visible := grouped[parentID]
		if visible == nil {
			visible = []*materializedRecord{}
		}
		for _, parent := range occurrences {
			parent.attach(job.node.name, materializedExpandValue{many: visible, plural: true})
		}
	}
	result := make([]*materializedRecord, len(sourceIDs))
	for index, id := range sourceIDs {
		result[index] = targets[id]
	}
	return result, nil
}

func discoverReverseExpand(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	node expandNode,
	parentIDs []uuid.UUID,
	maximumRows int,
	principal Principal,
	ruleContext *accessrule.RequestContext,
) ([]reverseExpandPair, error) {
	if maximumRows < 0 {
		return nil, expandBudgetError()
	}
	if _, err := relation.Bind(catalog, node.target); err != nil {
		return nil, relationHydrationFailure(err)
	}
	if err := recordaccess.AuthorizeSubject(ctx, tx, node.target.AccessCollectionID(), principal, "view"); err != nil {
		return nil, err
	}
	statement, err := compileReverseExpandDiscovery(node, parentIDs, maximumRows+1)
	if err != nil {
		return nil, err
	}
	if principal.Kind == identity.User || principal.Kind == identity.Anonymous {
		rules, _, err := recordaccess.LoadRules(ctx, tx, node.target.AccessCollectionID())
		if err != nil {
			return nil, err
		}
		policyTarget, err := sourcePolicyCollection(catalog, node.target)
		if err != nil {
			return nil, err
		}
		predicate, args, err := compilePolicyPredicateWithRequest(rules.View, node.target, policyTarget, "source_records", principal, len(statement.Args)-1, catalog, ruleContext)
		if err != nil {
			return nil, err
		}
		limit := statement.Args[len(statement.Args)-1]
		statement.Args = append(statement.Args[:len(statement.Args)-1], args...)
		statement.Args = append(statement.Args, limit)
		statement.SQL = strings.Replace(statement.SQL, " LIMIT $2", fmt.Sprintf(" LIMIT $%d", len(statement.Args)), 1)
		statement.SQL = injectBeforeOrder(statement.SQL, " AND ("+predicate+")")
	}
	rows, err := tx.Query(ctx, statement.SQL, statement.Args...)
	if err != nil {
		return nil, expansionTargetFailure(err)
	}
	pairs := make([]reverseExpandPair, 0)
	requested := make(map[uuid.UUID]struct{}, len(parentIDs))
	for _, id := range parentIDs {
		requested[id] = struct{}{}
	}
	seen := make(map[reverseExpandPair]struct{})
	for rows.Next() {
		var pair reverseExpandPair
		if err := rows.Scan(&pair.sourceID, &pair.parentID); err != nil {
			rows.Close()
			return nil, expansionTargetFailure(recordScanError(err))
		}
		if pair.sourceID == uuid.Nil || pair.parentID == uuid.Nil {
			rows.Close()
			return nil, schema.ErrConflict
		}
		if _, ok := requested[pair.parentID]; !ok {
			rows.Close()
			return nil, schema.ErrConflict
		}
		if _, duplicate := seen[pair]; duplicate {
			rows.Close()
			return nil, schema.ErrConflict
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, expansionTargetFailure(err)
	}
	rows.Close()
	return pairs, nil
}

func injectBeforeOrder(sql, predicate string) string {
	index := strings.LastIndex(sql, " ORDER BY ")
	if index < 0 {
		return sql + predicate
	}
	return sql[:index] + predicate + sql[index:]
}

func compileReverseExpandDiscovery(node expandNode, parentIDs []uuid.UUID, limit int) (Statement, error) {
	if node.kind != expandNodeReverse || node.field.Relation == nil || len(parentIDs) == 0 || limit <= 0 {
		return Statement{}, schema.ErrConflict
	}
	args := []any{parentIDs}

	var sql string
	switch node.field.Relation.Storage.Kind {
	case schema.RelationStorageInlineFKV1:
		idSQL := `source_records.` + quoted(systemFieldID)
		table := pgx.Identifier{"app", node.target.TableName}.Sanitize()
		field := `source_records.` + quoted(node.field.ColumnName)
		sql = "SELECT " + idSQL + ", " + field + " FROM " + table + " AS source_records" +
			" WHERE " + field + " = any($1::uuid[])" +
			" ORDER BY " + field + ", " + idSQL
	case schema.RelationStorageEdgeTableV1:
		idSQL := `source_records.` + quoted(systemFieldID)
		sourceTable := pgx.Identifier{"app", node.target.TableName}.Sanitize()
		edgeTable := pgx.Identifier{"pgbase_relation", node.field.Relation.Storage.Name}.Sanitize()
		sql = "SELECT relation_edges.source_id, relation_edges.target_id FROM " + edgeTable + " AS relation_edges" +
			" JOIN " + sourceTable + " AS source_records ON " + idSQL + " = relation_edges.source_id" +
			" WHERE relation_edges.target_id = any($1::uuid[])" +
			" ORDER BY relation_edges.target_id, relation_edges.source_id"
	default:
		return Statement{}, schema.ErrConflict
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" LIMIT $%d", len(args))
	return Statement{SQL: sql, Args: args}, nil
}
