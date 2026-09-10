package query

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
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
}

func (p rootPlan) countStatement() (Statement, error) {
	statement, err := p.pageStatement(nil, 1)
	if err != nil {
		return Statement{}, err
	}
	from := strings.Index(statement.SQL, " FROM ")
	order := strings.LastIndex(statement.SQL, " ORDER BY ")
	if from < 0 || order < from || len(statement.Args) == 0 {
		return Statement{}, errors.New("compile record count")
	}
	alias := p.order().root.TableName
	if p.order().relationAnchor {
		alias = relationRootAlias
	}
	statement.SQL = "SELECT COUNT(DISTINCT " + qualified(alias, systemFieldID) + ")" + statement.SQL[from:order]
	statement.Args = statement.Args[:len(statement.Args)-1]
	return statement, nil
}

func plannedRootPlan(filter filterNode, relation relationListPlan) rootPlan {
	relation.order.relationAnchor = relationListPlanUsesRelation(relation)
	return rootPlan{filter: filter, relation: relation}
}

func (p rootPlan) order() orderSpec { return p.relation.order }

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

func (s orderSpec) reloadAnchor(ctx context.Context, tx pgx.Tx, id uuid.UUID, membership *int64) ([]json.RawMessage, error) {
	if s.relationAnchor {
		statement, err := s.anchorStatement(id, membership)
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
