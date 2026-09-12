package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"slices"

	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/relation"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func prepareRelationWrite(
	catalog schema.Catalog,
	collection schema.Collection,
	mode relation.WriteMode,
	payload map[string]json.RawMessage,
) (*relation.Write, error) {
	write, err := relation.PrepareWrite(catalog, collection, mode, payload)
	if err != nil {
		return nil, normalizeRelationError(err)
	}
	return write, nil
}

func policyCollection(collection schema.Collection) schema.Collection {
	collection.ID = collection.AccessCollectionID()
	return collection
}

func authorizeRelationTargets(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, write *relation.Write) error {
	if principal.Kind == PrincipalAdministrator {
		return nil
	}
	byCollection := map[uuid.UUID][]uuid.UUID{}
	for _, field := range collection.Fields {
		if field.Type != schema.TypeRelation || field.Relation == nil {
			continue
		}
		raw, ok := write.Scalar()[field.Name]
		if !ok || string(raw) == "null" {
			continue
		}
		var ids []uuid.UUID
		if field.Relation.Cardinality == schema.RelationSingle {
			var id uuid.UUID
			if json.Unmarshal(raw, &id) != nil {
				return schema.ErrConflict
			}
			ids = []uuid.UUID{id}
		} else if json.Unmarshal(raw, &ids) != nil {
			return schema.ErrConflict
		}
		byCollection[field.Relation.TargetCollectionID] = append(byCollection[field.Relation.TargetCollectionID], ids...)
	}
	collections := catalogCollectionsByID(catalog)
	for collectionID, ids := range byCollection {
		target, ok := collections[collectionID]
		if !ok {
			return schema.ErrConflict
		}
		seen := map[uuid.UUID]struct{}{}
		unique := ids[:0]
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				unique = append(unique, id)
			}
		}
		ids = unique
		if err := recordaccess.AuthorizeSubject(ctx, tx, target.AccessCollectionID(), principal, "view"); err != nil {
			return err
		}
		if principal.Kind == identity.Service {
			continue
		}
		allowed, err := recordaccess.AllowsRecordsInCatalog(ctx, tx, catalog, policyCollection(target), principal, "view", ids, false)
		if err != nil {
			return err
		}
		if !allowed {
			return query.ErrRecordNotFound
		}
	}
	return nil
}

func authorizeStoredRelationTargets(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, principal Principal, record map[string]any) error {
	payload := map[string]json.RawMessage{}
	for _, field := range collection.Fields {
		if field.Type != schema.TypeRelation {
			continue
		}
		raw, err := json.Marshal(record[field.Name])
		if err != nil {
			return schema.ErrConflict
		}
		payload[field.Name] = raw
	}
	write, err := prepareRelationWrite(catalog, collection, relation.PatchWrite, payload)
	if err != nil {
		return err
	}
	return authorizeRelationTargets(ctx, tx, catalog, collection, principal, write)
}

func bindRecordRelations(catalog schema.Catalog, collection schema.Collection) (*relation.Bindings, error) {
	if !slices.ContainsFunc(collection.Fields, func(field schema.Field) bool { return field.Type == schema.TypeRelation }) {
		return nil, nil
	}
	bindings, err := relation.Bind(catalog, collection)
	if err != nil {
		return nil, schema.ErrConflict
	}
	return bindings, nil
}

func normalizeRelationError(err error) error {
	if err == nil {
		return nil
	}
	var validation *relation.ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	if errors.Is(err, relation.ErrSourceNotFound) {
		return query.ErrRecordNotFound
	}
	if errors.Is(err, relation.ErrInvalidBinding) || errors.Is(err, relation.ErrInvalidStoredValue) {
		return schema.ErrConflict
	}
	return err
}
