package recordops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/membership"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
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
		Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand,
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return RecordPage{}, unavailableMembership(err)
	}
	return page, err
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
		Collection: request.Collection, ID: request.ID, Expand: request.Expand})
}
