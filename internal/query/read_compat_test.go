package query

import (
	"context"

	"github.com/google/uuid"
)

var testReadPrincipal = Principal{
	Kind: PrincipalAdministrator,
	ID:   uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
}

type RelationListRequest struct {
	Principal  Principal
	Collection string
	Cursor     string
	Limit      int
	Filter     string
	Sort       string
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
