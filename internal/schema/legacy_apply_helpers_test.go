package schema

import (
	"context"

	"github.com/google/uuid"
)

// The unguarded HTTP apply is the only production caller of the legacy intent,
// and these show what it does: seal the change, bind it to the plan the caller
// approved, and read the collection back off the result. Preview needs no
// helper — the change constructor is the whole call.

func applyCreate(ctx context.Context, s *Service, input CreateCollectionInput, plan Plan, actor uuid.UUID) (Collection, error) {
	result, err := s.Apply(ctx, LegacyApply(NewCreateChange(input), plan, actor))
	return result.Collection, err
}

func applyUpdate(ctx context.Context, s *Service, input UpdateCollectionInput, plan Plan, actor uuid.UUID) (Collection, error) {
	result, err := s.Apply(ctx, LegacyApply(NewUpdateChange(input), plan, actor))
	return result.Collection, err
}

func applyDelete(ctx context.Context, s *Service, name string, plan Plan, actor uuid.UUID) error {
	_, err := s.Apply(ctx, LegacyApply(NewDeleteChange(name), plan, actor))
	return err
}
