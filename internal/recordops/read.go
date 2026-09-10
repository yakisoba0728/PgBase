package recordops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
)

func (s *Service) List(ctx context.Context, request ListRequest) (page RecordPage, err error) {
	started := time.Now()
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		if readTouchesRelationField(request.Expand, request.Filter, request.Sort) {
			s.record(ctx, observability.OperationEvent(relationReadOperation(request.Expand),
				relationOperationOutcome(err), time.Since(started)))
		}
		return RecordPage{}, err
	}
	page, err = s.reader.List(ctx, query.ListRequest{
		Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
		Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand, Fields: request.Fields,
		PocketBaseCompatibility: request.PocketBaseCompatibility,
		RuleContext:             request.RuleContext,
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return RecordPage{}, unavailableMembership(err)
	}
	return page, err
}

func (s *Service) ListPocketBase(ctx context.Context, request PocketBaseListRequest) (PocketBasePage, error) {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return PocketBasePage{}, err
	}
	return query.ListPocketBase(ctx, s.reader, query.PocketBaseListRequest{ListRequest: query.ListRequest{
		Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
		Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand, Fields: request.Fields,
		PocketBaseCompatibility: true, RuleContext: request.RuleContext,
	}, Page: request.Page, PerPage: request.PerPage, SkipTotal: request.SkipTotal})
}

func (s *Service) Count(ctx context.Context, request ListRequest) (int64, error) {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return 0, err
	}
	return query.Count(ctx, s.reader, query.ListRequest{Principal: request.Principal, Collection: request.Collection, Filter: request.Filter, Sort: request.Sort, PocketBaseCompatibility: request.PocketBaseCompatibility})
}

func (s *Service) Get(ctx context.Context, request GetRequest) (record Record, err error) {
	operationStarted := time.Now()
	relationOperation := readTouchesRelationField(request.Expand, "", "")
	defer s.finishRelationRecordOperation(ctx, relationReadOperation(request.Expand), operationStarted, &err, &relationOperation)
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return Record{}, err
	}
	if request.ID == uuid.Nil {
		return Record{}, &Error{Code: CodeInvalidRequest}
	}
	return s.reader.Get(ctx, query.GetRequest{Principal: request.Principal,
		Collection: request.Collection, ID: request.ID, Expand: request.Expand, Fields: request.Fields,
		PocketBaseCompatibility: request.PocketBaseCompatibility, RuleContext: request.RuleContext})
}
