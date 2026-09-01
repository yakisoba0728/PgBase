package query

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type rootPlan struct {
	collection    schema.Collection
	filter        filterNode
	requestedSort []SortKey
	sortKeys      []SortKey
	relation      *relationListPlan
	privateCursor bool
}

func scalarRootPlan(collection schema.Collection, filter filterNode, requestedSort []SortKey) rootPlan {
	return rootPlan{
		collection: collection, filter: filter, requestedSort: requestedSort,
		sortKeys: effectiveSortKeys(requestedSort),
	}
}

func plannedRootPlan(collection schema.Collection, filter filterNode, requestedSort []SortKey, relation relationListPlan) rootPlan {
	if !relationListPlanUsesRelation(relation) {
		return scalarRootPlan(collection, filter, requestedSort)
	}
	return rootPlan{
		collection: collection, filter: filter, requestedSort: requestedSort,
		sortKeys: relationPlanSortKeys(relation), relation: &relation,
		privateCursor: relation.privateCursor,
	}
}

func (p rootPlan) pageStatement(after *PageCursor, fetchLimit int) (Statement, error) {
	if p.relation != nil {
		return p.relation.pageStatement(after, fetchLimit)
	}
	return compileListPage(p.collection, ListQuery{
		Filter: p.filter, Sort: p.requestedSort, After: after,
	}, fetchLimit)
}

func (p rootPlan) anchorValues(ctx context.Context, tx pgx.Tx, id uuid.UUID) ([]json.RawMessage, error) {
	if p.relation != nil {
		statement, err := p.relation.anchorStatement(id)
		if err != nil {
			return nil, err
		}
		values, err := scanPlannedSortValues(tx.QueryRow(ctx, statement.SQL, statement.Args...), p.relation.sort)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return values, err
	}
	statement := CompileSelectByID(p.collection, id)
	anchor, err := EncodeRecord(p.collection, tx.QueryRow(ctx, statement.SQL, statement.Args...))
	if err := recordQueryError(err); err != nil {
		return nil, err
	}
	return recordSortValues(p.sortKeys, anchor)
}

func (p rootPlan) scan(row RowScanner) (pageCandidate, error) {
	if p.relation == nil {
		record, err := EncodeRecord(p.collection, row)
		if err != nil {
			return pageCandidate{}, err
		}
		values, err := recordSortValues(p.sortKeys, record)
		if err != nil {
			return pageCandidate{}, err
		}
		return pageCandidate{root: record, wire: record, cursorValues: values}, nil
	}

	scanner, err := newRelationRecordScanner(row, p.relation.sort)
	if err != nil {
		return pageCandidate{}, err
	}
	record, err := EncodeRecord(p.collection, scanner)
	if err != nil {
		return pageCandidate{}, err
	}
	values, err := scanner.cursorValues(record)
	if err != nil {
		return pageCandidate{}, err
	}
	return pageCandidate{root: record, wire: record, cursorValues: values}, nil
}
