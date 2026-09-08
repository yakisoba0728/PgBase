package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/identity"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
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
	principal Principal,
	usage *expansionUsage,
) error {
	if len(records) == 0 {
		return nil
	}
	if multipleRelationFieldCount(collection) == 0 {
		tx, ok := querier.(pgx.Tx)
		if !ok {
			return relationHydrationFailure(relation.ErrInvalidBinding)
		}
		return filterRelationValues(ctx, tx, catalog, collection, records, principal)
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
	if err := usage.takeRows(used); err != nil {
		return err
	}
	tx, ok := querier.(pgx.Tx)
	if !ok {
		return relationHydrationFailure(relation.ErrInvalidBinding)
	}
	return filterRelationValues(ctx, tx, catalog, collection, records, principal)
}

func filterRelationValues(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, records []map[string]any, principal Principal) error {
	if principal.Kind == identity.Administrator {
		return nil
	}
	visible := map[uuid.UUID]map[uuid.UUID]bool{}
	all := map[uuid.UUID][]uuid.UUID{}
	for _, field := range collection.Fields {
		if field.Type != schema.TypeRelation || field.Relation == nil {
			continue
		}
		for _, record := range records {
			refs, err := relationIDs(field, record)
			if err != nil {
				return err
			}
			all[field.Relation.TargetCollectionID] = append(all[field.Relation.TargetCollectionID], refs.ids...)
		}
	}
	for targetID, ids := range all {
		target, ok := catalogCollectionByID(catalog, targetID)
		if !ok {
			return schema.ErrConflict
		}
		if err := recordaccess.AuthorizeSubject(ctx, tx, target.AccessCollectionID(), principal, "view"); err != nil {
			return err
		}
		set := map[uuid.UUID]bool{}
		visible[targetID] = set
		if principal.Kind == identity.Service {
			for _, id := range ids {
				set[id] = true
			}
			continue
		}
		rules, _, err := recordaccess.LoadRules(ctx, tx, target.AccessCollectionID())
		if err != nil {
			return err
		}
		policyTarget, err := sourcePolicyCollection(catalog, target)
		if err != nil {
			return err
		}
		predicate, args, err := compilePolicyPredicate(rules.View, target, policyTarget, target.TableName, principal, 1)
		if err != nil {
			return err
		}
		args = append([]any{ids}, args...)
		rows, err := tx.Query(ctx, "SELECT id FROM "+quotedTable(target)+" WHERE id=ANY($1::uuid[]) AND ("+predicate+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			if rows.Scan(&id) != nil {
				rows.Close()
				return schema.ErrConflict
			}
			set[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	for _, field := range collection.Fields {
		if field.Type != schema.TypeRelation || field.Relation == nil {
			continue
		}
		set := visible[field.Relation.TargetCollectionID]
		for _, record := range records {
			refs, err := relationIDs(field, record)
			if err != nil {
				return err
			}
			if field.Relation.Cardinality == schema.RelationSingle {
				if len(refs.ids) == 1 && !set[refs.ids[0]] {
					record[field.Name] = nil
				}
				continue
			}
			values := make([]string, 0, len(refs.ids))
			for _, id := range refs.ids {
				if set[id] {
					values = append(values, id.String())
				}
			}
			record[field.Name] = values
		}
	}
	return nil
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
