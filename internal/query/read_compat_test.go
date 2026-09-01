package query

import (
	"context"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const PrincipalAdministrator = identity.Administrator

var testReadPrincipal = Principal{
	Kind: PrincipalAdministrator,
	ID:   uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
}

type ListQuery struct {
	Filter filterNode
	Sort   []SortKey
	After  *PageCursor
	Limit  int
}

func scalarRootPlan(collection schema.Collection, filter filterNode, requestedSort []SortKey) (rootPlan, error) {
	order, err := resolveOrderSpec(collection, effectiveSortKeys(requestedSort))
	if err != nil {
		return rootPlan{}, err
	}
	return rootPlan{filter: filter, relation: relationListPlan{order: order}}, nil
}

func CompileList(collection schema.Collection, query ListQuery) (Statement, error) {
	order, err := resolveOrderSpec(collection, effectiveSortKeys(query.Sort))
	if err != nil {
		return Statement{}, err
	}
	return compileListPage(query.Filter, order, query.After, normalizedFetchLimit(query.Limit))
}

func (p relationListPlan) statement(after *PageCursor, limit int) (Statement, error) {
	return p.pageStatement(after, normalizedFetchLimit(limit))
}

type RelationListRequest struct {
	Principal  Principal
	Collection string
	Cursor     string
	Limit      int
	Filter     string
	Sort       string
}

type ExpandedRecordPage struct {
	Items      []ExpandedRecord `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`

	querySteps int
}

func (s *Service) listScalar(
	ctx context.Context,
	collection, cursor string,
	limit int,
	filter, sort string,
) (RecordPage, error) {
	return s.List(ctx, ListRequest{
		Principal: testReadPrincipal, Collection: collection, Cursor: cursor,
		Limit: limit, Filter: filter, Sort: sort,
	})
}

func (s *Service) ListRelationAware(ctx context.Context, request RelationListRequest) (RecordPage, error) {
	if !request.Principal.Valid() {
		request.Principal = testReadPrincipal
	}
	return s.List(ctx, ListRequest{
		Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
		Limit: request.Limit, Filter: request.Filter, Sort: request.Sort,
	})
}

func (s *Service) ListExpanded(ctx context.Context, request ListRequest) (ExpandedRecordPage, error) {
	result, err := s.readList(ctx, request)
	if err != nil {
		return ExpandedRecordPage{}, err
	}
	return ExpandedRecordPage{
		Items: result.expanded, NextCursor: result.page.NextCursor, querySteps: result.querySteps,
	}, nil
}

func (s *Service) GetExpanded(ctx context.Context, request GetRequest) (ExpandedRecord, error) {
	result, err := s.readGet(ctx, request)
	return result.expanded, err
}
