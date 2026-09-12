package recordops

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) resolveRecordID(ctx context.Context, collectionName string, internal uuid.UUID, public string) (uuid.UUID, error) {
	var db recordidentity.DB = s.pool
	var catalog schema.Catalog
	var err error
	if tx, ok := ctx.Value(batchTransactionKey{}).(pgx.Tx); ok {
		db = tx
		catalog, err = s.schemas.EnsureCurrent(ctx, tx)
	} else {
		catalog, err = s.schemas.Catalog(ctx)
	}
	if err != nil {
		return uuid.Nil, err
	}
	collection, ok := catalog.Collections[collectionName]
	if !ok {
		return uuid.Nil, query.ErrCollectionNotFound
	}
	if public != "" {
		id, err := s.identities.Resolve(ctx, db, collection, public)
		return id, normalizeIdentityMutationError(err)
	}
	if collection.RecordID.Kind == schema.RecordIDText || internal == uuid.Nil {
		return uuid.Nil, &Error{Code: CodeInvalidRecordID}
	}
	return internal, nil
}

func recordIdentityInput(collection schema.Collection, input map[string]json.RawMessage, preset uuid.UUID) (map[string]json.RawMessage, uuid.UUID, *string, error) {
	clean := cloneRawInput(input)
	raw, present := clean["id"]
	delete(clean, "id")
	if !present {
		return clean, preset, nil, nil
	}
	var public string
	if json.Unmarshal(raw, &public) != nil || public == "" {
		return nil, uuid.Nil, nil, &Error{Code: CodeInvalidRecordID}
	}
	if collection.RecordID.Kind == schema.RecordIDText {
		return clean, preset, &public, nil
	}
	id, err := uuid.Parse(public)
	if err != nil || id == uuid.Nil || preset != uuid.Nil && preset != id {
		return nil, uuid.Nil, nil, &Error{Code: CodeInvalidRecordID}
	}
	return clean, id, &public, nil
}

func (s *Service) projectRecordIdentity(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, data map[string]any) error {
	if err := s.identities.Project(ctx, tx, catalog, collection, data); err != nil {
		return normalizeIdentityMutationError(err)
	}
	return nil
}

func normalizeIdentityMutationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, recordidentity.ErrInvalid) {
		return &Error{Code: CodeInvalidRecordID}
	}
	if errors.Is(err, recordidentity.ErrConflict) {
		return &Error{Code: CodeRecordIDConflict}
	}
	if errors.Is(err, recordidentity.ErrNotFound) {
		return query.ErrRecordNotFound
	}
	return err
}

func cloneRecordData(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func (s *Service) resolveRelationIdentities(ctx context.Context, tx recordidentity.DB, catalog schema.Catalog, collection schema.Collection, input map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	result := cloneRawInput(input)
	collections := catalogCollectionsByID(catalog)
	for _, field := range collection.Fields {
		if field.Relation == nil {
			continue
		}
		raw, present := result[field.Name]
		if !present || string(raw) == "null" {
			continue
		}
		target, ok := collections[field.Relation.TargetCollectionID]
		if !ok {
			return nil, schema.ErrConflict
		}
		var public []string
		if field.Relation.Cardinality == schema.RelationSingle {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, &Error{Code: CodeInvalidRecordID}
			}
			public = []string{value}
		} else if json.Unmarshal(raw, &public) != nil || public == nil {
			return nil, &Error{Code: CodeInvalidRecordID}
		}
		ids, err := s.identities.ResolveMany(ctx, tx, target, public)
		if err != nil {
			return nil, normalizeIdentityMutationError(err)
		}
		var encoded []byte
		if field.Relation.Cardinality == schema.RelationSingle {
			encoded, err = json.Marshal(ids[0].String())
		} else {
			values := make([]string, len(ids))
			for i := range ids {
				values[i] = ids[i].String()
			}
			encoded, err = json.Marshal(values)
		}
		if err != nil {
			return nil, err
		}
		result[field.Name] = encoded
	}
	return result, nil
}
