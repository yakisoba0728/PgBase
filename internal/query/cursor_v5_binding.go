package query

import (
	"errors"

	"github.com/webisnoteasy/PgBase/internal/cursor"
	"github.com/webisnoteasy/PgBase/internal/membership"
)

func (s *Service) newCursorV5Descriptor(plan rootPlan, traversal membership.Traversal) (*cursor.V5Descriptor, error) {
	if plan.catalogVersion < 1 || !plan.principal.Valid() ||
		traversal.ValidateV5(traversal.Generation, traversal.HighWater) != nil {
		return nil, invalidCursor()
	}
	queryHash, err := cursorV5QueryFingerprint(s.cursors, plan.relation.filter)
	if err != nil {
		return nil, cursorV5RequestError(err)
	}
	order, err := plan.order().cursorV5Order()
	if err != nil {
		return nil, cursorV5RequestError(err)
	}
	return &cursor.V5Descriptor{
		CatalogVersion:       plan.catalogVersion,
		MembershipGeneration: traversal.Generation,
		QueryHash:            queryHash,
		ScopeHash:            s.cursorV5ScopeFingerprint(plan.principal),
		Order:                order,
		HighWater:            traversal.HighWater,
	}, nil
}

func (s *Service) bindCursorV5(page cursor.Page, plan rootPlan, traversal membership.Traversal) (*int64, error) {
	if plan.catalogVersion < 1 || !plan.principal.Valid() || page.Version != cursor.Version5 || page.V5 == nil {
		return nil, invalidCursor()
	}
	expectedQuery, err := cursorV5QueryFingerprint(s.cursors, plan.relation.filter)
	if err != nil {
		return nil, cursorV5RequestError(err)
	}
	expectedOrder, err := plan.order().cursorV5Order()
	if err != nil {
		return nil, cursorV5RequestError(err)
	}
	if !page.V5.Matches(plan.catalogVersion, expectedQuery, s.cursorV5ScopeFingerprint(plan.principal), expectedOrder) {
		return nil, invalidCursor()
	}
	if traversal.ValidateV5(page.V5.MembershipGeneration, page.V5.HighWater) != nil {
		return nil, invalidCursor()
	}
	return &page.V5.HighWater, nil
}

func (s *Service) cursorV5ScopeFingerprint(principal Principal) []byte {
	policy := "administrator-all-v1"
	if principal.Kind == "user" {
		policy = "owner-only-v1"
	}
	return s.cursors.ScopeFingerprint([]byte(`{"kind":"` + string(principal.Kind) + `","id":"` + principal.ID.String() + `","policy":"` + policy + `"}`))
}

// A v5 descriptor is derived from the request's own filter and order, so a
// failure building one usually means the request is malformed, not that a
// cursor is bad. Reporting it as an invalid cursor would change the answer a
// cursorless request already had the moment v5 issuance is enabled.
func cursorV5RequestError(err error) error {
	var validationError *ValidationError
	if errors.As(err, &validationError) {
		return err
	}
	return invalidCursor()
}
