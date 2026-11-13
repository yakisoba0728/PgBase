package recordops

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) List(ctx context.Context, request ListRequest) (page RecordPage, err error) {
	started := time.Now()
	if err := s.validateRequest(ctx, request.Principal, request.RequestID); err != nil {
		if readTouchesRelationField(request.Expand, request.Filter, request.Sort) {
			s.record(ctx, observability.OperationEvent(relationReadOperation(request.Expand),
				relationOperationOutcome(err), time.Since(started)))
		}
		return RecordPage{}, err
	}
	err = s.pinnedTx(ctx, request.Collection, pgx.RepeatableRead, nil, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		var err error
		page, err = s.reader.List(WithTransaction(ctx, tx), query.ListRequest{
			Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
			Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand, Fields: request.Fields,
			PocketBaseCompatibility: request.PocketBaseCompatibility,
			RuleContext:             request.RuleContext, Total: request.Total,
		})
		if err != nil {
			return err
		}
		return s.identities.Project(ctx, tx, catalog, collection, page.Items...)
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return RecordPage{}, unavailableMembership(err)
	}
	if err != nil {
		return RecordPage{}, normalizeIdentityReadError(err)
	}
	return page, nil
}

func (s *Service) ListPocketBase(ctx context.Context, request PocketBaseListRequest) (page PocketBasePage, err error) {
	if err := s.validateRequest(ctx, request.Principal, request.RequestID); err != nil {
		return PocketBasePage{}, err
	}
	err = s.pinnedTx(ctx, request.Collection, pgx.RepeatableRead, nil, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		var err error
		page, err = query.ListPocketBase(WithTransaction(ctx, tx), s.reader, query.PocketBaseListRequest{ListRequest: query.ListRequest{
			Principal: request.Principal, Collection: request.Collection, Cursor: request.Cursor,
			Limit: request.Limit, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand, Fields: request.Fields,
			PocketBaseCompatibility: true, RuleContext: request.RuleContext,
		}, Page: request.Page, PerPage: request.PerPage, SkipTotal: request.SkipTotal})
		if err != nil {
			return err
		}
		return s.identities.Project(ctx, tx, catalog, collection, page.Items...)
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return PocketBasePage{}, unavailableMembership(err)
	}
	if err != nil {
		return PocketBasePage{}, normalizeIdentityReadError(err)
	}
	return page, nil
}

func (s *Service) Count(ctx context.Context, request ListRequest) (int64, error) {
	if err := s.validateRequest(ctx, request.Principal, request.RequestID); err != nil {
		return 0, err
	}
	total, err := s.reader.Count(ctx, query.ListRequest{
		Principal: request.Principal, Collection: request.Collection, Filter: request.Filter,
		Sort: request.Sort, PocketBaseCompatibility: request.PocketBaseCompatibility,
		RuleContext: request.RuleContext,
	})
	if errors.Is(err, membership.ErrUnavailable) {
		return 0, unavailableMembership(err)
	}
	return total, err
}

func (s *Service) Get(ctx context.Context, request GetRequest) (record Record, err error) {
	operationStarted := time.Now()
	relationOperation := readTouchesRelationField(request.Expand, "", "")
	defer s.finishRelationRecordOperation(ctx, relationReadOperation(request.Expand), operationStarted, &err, &relationOperation)
	if err := s.validateRequest(ctx, request.Principal, request.RequestID); err != nil {
		return Record{}, err
	}
	err = s.pinnedTx(ctx, request.Collection, pgx.RepeatableRead, nil, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		id := request.ID
		var err error
		if request.PublicID != "" {
			id, err = s.identities.Resolve(ctx, tx, collection, request.PublicID)
			if err != nil {
				return normalizeIdentityReadError(err)
			}
		} else if collection.RecordID.Kind == schema.RecordIDText {
			return &Error{Code: CodeInvalidRecordID}
		}
		if id == uuid.Nil {
			return &Error{Code: CodeInvalidRequest}
		}
		record, err = s.reader.Get(WithTransaction(ctx, tx), query.GetRequest{Principal: request.Principal,
			Collection: request.Collection, ID: id, Expand: request.Expand, Fields: request.Fields,
			PocketBaseCompatibility: request.PocketBaseCompatibility, RuleContext: request.RuleContext})
		if err != nil {
			return err
		}
		return s.identities.Project(ctx, tx, catalog, collection, record.Data)
	})
	if err != nil {
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
