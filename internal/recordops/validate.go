package recordops

import (
	"context"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
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
	if request.ID == uuid.Nil {
		resolved, resolveErr := resolveInputModifiers(collection, request.Input, modifierCreateValues(collection))
		if resolveErr != nil {
			return resolveErr
		}
		_, err = query.CompileInsert(collection, resolved, uuid.New())
		return err
	}
	return validateModifierInput(collection, request.Input)
}
