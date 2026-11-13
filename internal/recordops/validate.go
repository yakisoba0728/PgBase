package recordops

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Validate checks the typed payload accepted by Create/Update without writing.
func (s *Service) Validate(ctx context.Context, request ValidateRequest) error {
	if err := s.validateRequest(ctx, request.Principal, request.RequestID); err != nil {
		return err
	}
	err := s.pinnedTx(ctx, request.Collection, pgx.RepeatableRead, nil, func(tx pgx.Tx, catalog schema.Catalog, collection schema.Collection) error {
		if collection.IsView() {
			return recordaccess.ErrUnsupported
		}
		if err := rejectHiddenInput(collection, request.Principal, request.Input); err != nil {
			return err
		}
		input, recordID, publicID, err := recordIdentityInput(collection, request.Input, request.ID)
		if err != nil {
			return err
		}
		if request.PublicID != "" {
			recordID, err = s.resolveRecordID(WithTransaction(ctx, tx), request.Collection, request.ID, request.PublicID)
			if err != nil {
				return err
			}
		}
		if recordID == uuid.Nil {
			if publicID != nil && schema.ValidateRecordIDValue(collection.RecordID, *publicID) != "" {
				return &Error{Code: CodeInvalidRecordID}
			}
			resolved, resolveErr := resolveInputModifiers(collection, input, modifierCreateValues(collection))
			if resolveErr != nil {
				return resolveErr
			}
			validationCollection, resolveErr := generatedValidationCollection(collection, resolved, true)
			if resolveErr != nil {
				return resolveErr
			}
			resolved, resolveErr = s.resolveRelationIdentities(ctx, tx, catalog, collection, resolved)
			if resolveErr != nil {
				return resolveErr
			}
			_, err = query.CompileInsert(validationCollection, resolved, uuid.New())
			return err
		}
		_, err = generatedValidationCollection(collection, input, false)
		if err != nil {
			return err
		}
		resolved, err := s.resolveRelationIdentities(ctx, tx, catalog, collection, input)
		if err != nil {
			return err
		}
		return validateModifierInput(collection, resolved)
	})
	if errors.Is(err, query.ErrCollectionNotFound) {
		return recordaccess.ErrUnsupported
	}
	return err
}
