package query

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/membership"
)

func (s *Service) bindCursorV5(ctx context.Context, tx pgx.Tx, page cursor.Page, plan rootPlan) (*int64, error) {
	if plan.catalogVersion < 1 || !plan.principal.Valid() || page.Version != cursor.Version5 || page.V5 == nil {
		return nil, invalidCursor()
	}
	expectedQuery, queryErr := cursorV5QueryFingerprint(s.cursors, plan.relation.filter)
	expectedScope := s.cursors.ScopeFingerprint([]byte(`{"kind":"administrator","id":"` + plan.principal.ID.String() + `","policy":"administrator-all-v1"}`))
	expectedOrder, orderErr := plan.order().cursorV5Order()
	if queryErr != nil || orderErr != nil ||
		!page.V5.Matches(plan.catalogVersion, expectedQuery, expectedScope, expectedOrder) {
		return nil, invalidCursor()
	}
	if membership.ValidateTraversal(ctx, tx, plan.order().root.ID, page.V5.MembershipGeneration, page.V5.HighWater) != nil {
		return nil, invalidCursor()
	}
	return &page.V5.HighWater, nil
}
