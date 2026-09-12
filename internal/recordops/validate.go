package recordops

import (
	"context"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Validate checks the typed payload accepted by Create/Update without writing.
func (s *Service) Validate(ctx context.Context, request ValidateRequest) error {
	if err := s.validateRequest(request.Principal, request.RequestID); err != nil {
		return err
	}
	catalog, err := s.schemas.Catalog(ctx)
	if err != nil {
		return err
	}
	collection, ok := catalog.Collections[request.Collection]
	if !ok || collection.IsView() {
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
		recordID, err = s.resolveRecordID(ctx, request.Collection, request.ID, request.PublicID)
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
		resolved, resolveErr = s.resolveRelationIdentities(ctx, s.pool, catalog, collection, resolved)
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
	resolved, err := s.resolveRelationIdentities(ctx, s.pool, catalog, collection, input)
	if err != nil {
		return err
	}
	return validateModifierInput(collection, resolved)
}
