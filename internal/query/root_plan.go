package query

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rootPlan struct {
	filter         filterNode
	relation       relationListPlan
	catalogVersion int64
	principal      Principal
	ownerID        *uuid.UUID
}

func plannedRootPlan(filter filterNode, relation relationListPlan) rootPlan {
	relation.order.relationAnchor = relationListPlanUsesRelation(relation)
	return rootPlan{filter: filter, relation: relation}
}

func (p rootPlan) order() orderSpec { return p.relation.order }

func (p rootPlan) pageStatement(after *pageBoundary, fetchLimit int) (Statement, error) {
	if p.order().relationAnchor {
		return p.relation.pageStatement(after, fetchLimit)
	}
	return compileListPage(p.filter, p.order(), after, fetchLimit, p.ownerID)
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
