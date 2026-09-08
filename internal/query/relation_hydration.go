package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type relationHydrationError struct{ cause error }

func (e *relationHydrationError) Error() string { return "hydrate relation values failed" }
func (e *relationHydrationError) Unwrap() error { return e.cause }

func relationHydrationFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, relation.ErrInvalidBinding) || errors.Is(err, relation.ErrInvalidStoredValue) || isCatalogDriftFailure(err) {
		err = schema.ErrConflict
	}
	return &relationHydrationError{cause: err}
}

// hydrateRelationRecordsBounded constrains junction work in SQL and charges
// every consumed edge to the request-wide relation-row budget.
func hydrateRelationRecordsBounded(
	ctx context.Context,
	querier relation.RowsQuerier,
	catalog schema.Catalog,
	collection schema.Collection,
	records []map[string]any,
	usage *expansionUsage,
) error {
	if len(records) == 0 || multipleRelationFieldCount(collection) == 0 {
		return nil
	}
	bindings, err := relation.Bind(catalog, collection)
	if err != nil {
		return relationHydrationFailure(err)
	}
	ids, byID, err := indexRelationRecords(records)
	if err != nil {
		return err
	}
	remaining := usage.limits.maxRows - usage.rows
	if remaining < 0 {
		return expandBudgetError()
	}
	used, err := bindings.HydrateBounded(ctx, querier, ids, byID, remaining)
	if errors.Is(err, relation.ErrHydrationBudgetExceeded) {
		return expandBudgetError()
	}
	if err != nil {
		return relationHydrationFailure(err)
	}
	return usage.takeRows(used)
}

func indexRelationRecords(records []map[string]any) ([]uuid.UUID, map[uuid.UUID]map[string]any, error) {
	byID := make(map[uuid.UUID]map[string]any, len(records))
	ids := make([]uuid.UUID, 0, len(records))
	for _, record := range records {
		text, ok := record[systemFieldID].(string)
		if !ok {
			return nil, nil, errors.New("hydrate relation values: invalid record id")
		}
		id, err := uuid.Parse(text)
		if err != nil || id == uuid.Nil || id.String() != text {
			return nil, nil, errors.New("hydrate relation values: invalid record id")
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, nil, errors.New("hydrate relation values: duplicate record id")
		}
		byID[id] = record
		ids = append(ids, id)
	}
	return ids, byID, nil
}
