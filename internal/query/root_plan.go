package query

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type rootPlan struct {
	filter           filterNode
	relation         relationListPlan
	catalogVersion   int64
	principal        Principal
	ownerID          *uuid.UUID
	rule             *accessrule.Rule
	ruleRevision     int64
	policyCollection schema.Collection
	outputRuleScope  string
	ruleContext      *accessrule.RequestContext
	countLimit       int
}

func (p rootPlan) countStatement(memberships ...*int64) (Statement, error) {
	var boundary *pageBoundary
	if len(memberships) > 0 && memberships[0] != nil {
		boundary = &pageBoundary{membership: memberships[0], membershipOnly: true}
	}
	policy := ruleBinding{rule: p.rule, principal: p.principal, collection: p.policyCollection, catalog: p.relation.catalog, request: p.ruleContext}
	alias := p.order().root.TableName
	var statement Statement
	var err error
	if p.order().relationAnchor {
		statement, _, err = p.relation.rowsStatementWithPolicy(boundary, p.ownerID, policy)
		alias = relationRootAlias
	} else {
		statement, err = compileListRows(p.filter, p.order(), boundary, p.ownerID, policy)
	}
	if err != nil {
		return Statement{}, err
	}
	// Without relation sort joins the rows are the root table (id is its primary
	// key, or a view identity validated unique in this transaction) filtered by
	// EXISTS predicates, so they cannot repeat an id and need no DISTINCT. A
	// relation sort may LEFT JOIN a target whose id is not guaranteed unique.
	rows := statement.SQL
	selected, counted := "1", "*"
	for _, term := range p.order().terms {
		if term.relationPath != nil && len(term.relationPath.hops) > 0 {
			identity := qualified(alias, systemFieldID)
			selected, counted = "DISTINCT "+identity, "DISTINCT "+identity
			break
		}
	}
	statement.SQL = "SELECT COUNT(" + counted + ")" + rows
	if p.countLimit > 0 {
		statement.SQL = "SELECT COUNT(*) FROM (SELECT " + selected + rows + " LIMIT " + strconv.Itoa(p.countLimit) + ") bounded_count"
	}
	return statement, nil
}

func plannedRootPlan(filter filterNode, relation relationListPlan) rootPlan {
	relation.order.relationAnchor = relationListPlanUsesRelation(relation)
	return rootPlan{filter: filter, relation: relation}
}

// order returns the plan's order with cursor privacy bound to the principal.
// Only administrators see every relation target, so every other principal
// receives anchor-only cursors for bare relation sorts: the stored target id
// must not reach the client through a cursor when the item itself redacts it.
func (p rootPlan) order() orderSpec {
	order := p.relation.order
	if p.principal.Kind != identity.Administrator && order.sortsRelationKey() {
		order.relationKeyCursor = true
	}
	return order
}

func (p rootPlan) pageStatement(after *pageBoundary, fetchLimit int, offsets ...int) (Statement, error) {
	if p.order().relationAnchor {
		return p.relation.pageStatementWithPolicy(after, fetchLimit, p.ownerID, ruleBinding{rule: p.rule, principal: p.principal, collection: p.policyCollection, catalog: p.relation.catalog, request: p.ruleContext}, offsets...)
	}
	offset := 0
	if len(offsets) > 0 {
		offset = offsets[0]
	}
	return compileListPageOffset(p.filter, p.order(), after, fetchLimit, offset, p.ownerID, ruleBinding{rule: p.rule, principal: p.principal, collection: p.policyCollection, catalog: p.relation.catalog, request: p.ruleContext})
}

func (s orderSpec) reloadAnchor(ctx context.Context, tx pgx.Tx, id uuid.UUID, membership *int64, binding ruleBinding) ([]json.RawMessage, error) {
	if s.relationAnchor {
		statement, err := s.anchorStatement(id, membership, binding)
		if err != nil {
			return nil, err
		}
		values, err := s.scanValues(tx.QueryRow(ctx, statement.SQL, statement.Args...))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return values, err
	}
	statement := compileSelectByIDWithMembership(s.root, id, membership)
	anchor, err := EncodeRecord(s.root, tx.QueryRow(ctx, statement.SQL, statement.Args...))
	if err := recordQueryError(err); err != nil {
		return nil, err
	}
	return s.recordValues(anchor)
}

func (p rootPlan) scan(row RowScanner) (pageCandidate, error) {
	order := p.order()
	if !order.relationAnchor {
		record, err := EncodeRecord(order.root, row)
		if err != nil {
			return pageCandidate{}, err
		}
		values, err := order.recordValues(record)
		if err != nil {
			return pageCandidate{}, err
		}
		id, err := canonicalRecordID(record)
		if err != nil {
			return pageCandidate{}, err
		}
		return pageCandidate{id: id, root: record, wire: record, cursorValues: values}, nil
	}

	scanner, err := newRelationRecordScanner(row, order)
	if err != nil {
		return pageCandidate{}, err
	}
	record, err := EncodeRecord(order.root, scanner)
	if err != nil {
		return pageCandidate{}, err
	}
	values, err := scanner.cursorValues(record)
	if err != nil {
		return pageCandidate{}, err
	}
	id, err := canonicalRecordID(record)
	if err != nil {
		return pageCandidate{}, err
	}
	return pageCandidate{id: id, root: record, wire: record, cursorValues: values}, nil
}
