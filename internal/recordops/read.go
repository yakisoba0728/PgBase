package recordops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
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
	if err == nil {
		catalog, catalogErr := s.schemas.Catalog(ctx)
		collection, ok := catalog.Collections[request.Collection]
		if catalogErr != nil {
			return RecordPage{}, catalogErr
		}
		if !ok {
			return RecordPage{}, query.ErrCollectionNotFound
		}
		if projectErr := s.identities.Project(ctx, s.pool, catalog, collection, page.Items...); projectErr != nil {
			return RecordPage{}, normalizeIdentityReadError(projectErr)
		}
	}
	return page, err
}

func (s *Service) ListPocketBase(ctx context.Context, request PocketBaseListRequest) (PocketBasePage, error) {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return PocketBasePage{}, err
	}
	page, err := query.ListPocketBase(ctx, s.reader, query.PocketBaseListRequest{ListRequest: query.ListRequest{
		Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
		Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand, Fields: request.Fields,
		PocketBaseCompatibility: true, RuleContext: request.RuleContext,
	}, Page: request.Page, PerPage: request.PerPage, SkipTotal: request.SkipTotal})
	if err != nil {
		return PocketBasePage{}, err
	}
	catalog, err := s.schemas.Catalog(ctx)
	if err != nil {
		return PocketBasePage{}, err
	}
	collection, ok := catalog.Collections[request.Collection]
	if !ok {
		return PocketBasePage{}, query.ErrCollectionNotFound
	}
	if err := s.identities.Project(ctx, s.pool, catalog, collection, page.Items...); err != nil {
		return PocketBasePage{}, normalizeIdentityReadError(err)
	}
	return page, nil
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
	catalog, err := s.schemas.Catalog(ctx)
	if err != nil {
		return Record{}, err
	}
	collection, ok := catalog.Collections[request.Collection]
	if !ok {
		return Record{}, query.ErrCollectionNotFound
	}
	id := request.ID
	if request.PublicID != "" {
		id, err = s.identities.Resolve(ctx, s.pool, collection, request.PublicID)
		if err != nil {
			return Record{}, normalizeIdentityReadError(err)
		}
	} else if collection.RecordID.Kind == "text" {
		return Record{}, &Error{Code: CodeInvalidRecordID}
	}
	if id == uuid.Nil {
		return Record{}, &Error{Code: CodeInvalidRequest}
	}
	record, err = s.reader.Get(ctx, query.GetRequest{Principal: request.Principal,
		Collection: request.Collection, ID: id, Expand: request.Expand, Fields: request.Fields,
		PocketBaseCompatibility: request.PocketBaseCompatibility, RuleContext: request.RuleContext})
	if err != nil {
		return Record{}, err
	}
	if err := s.identities.Project(ctx, s.pool, catalog, collection, record.Data); err != nil {
		return Record{}, normalizeIdentityReadError(err)
	}
	return record, nil
}

func normalizeIdentityReadError(err error) error {
	if errors.Is(err, recordidentity.ErrInvalid) {
		return &Error{Code: CodeInvalidRecordID}
	}
	if errors.Is(err, recordidentity.ErrNotFound) {
		return query.ErrRecordNotFound
	}
	return err
}
