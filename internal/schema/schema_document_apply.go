package schema

import (
	"context"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Service) applyImportedChange(ctx context.Context, tx pgx.Tx, store *Store, catalog Catalog, change Change, plan Plan, actor uuid.UUID) error {
	now := time.Now().UTC()
	var collection Collection
	var current Collection
	var err error
	switch change.kind {
	case changeCreate:
		collection, err = newCollectionMetadata(plan.Definition)
		if err == nil {
			collection.CreatedAt = now
		}
	case changeUpdate:
		current = catalog.Collections[change.update.Name]
		if err := validateRequiredRelationAdds(ctx, tx, current, plan.Definition, true); err != nil {
			return err
		}
		fields, fieldErr := updatedFieldMetadata(current.ID, current.Fields, plan.Definition)
		if fieldErr != nil {
			return fieldErr
		}
		indexes, indexErr := updatedIndexMetadata(current.Name, current.Indexes, plan.Definition)
		if indexErr != nil {
			return indexErr
		}
		collection = Collection{ID: current.ID, Name: current.Name, CompatibilityName: plan.Definition.CompatibilityName, Type: plan.Definition.Type, System: plan.Definition.System, ValidationMode: plan.Definition.ValidationMode, TableName: current.TableName, Fields: fields, Indexes: indexes, View: resolvedViewSpec(plan.Definition.View), CreatedAt: current.CreatedAt}
	default:
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if change.kind == changeUpdate {
		if err := validateChangedFieldOptions(ctx, tx, current, collection); err != nil {
			return err
		}
	}
	if err := s.executePlan(ctx, tx, plan); err != nil {
		return err
	}
	if change.kind == changeUpdate {
		if err := validateChangedRecordSizes(ctx, tx, current, collection); err != nil {
			return err
		}
	}
	if change.kind == changeCreate {
		err = store.InsertCollection(ctx, collection, plan.Definition, now)
	} else {
		err = store.UpdateCollection(ctx, collection, plan.Definition, now)
	}
	if err != nil {
		return err
	}
	operationID := uuid.New()
	checksum, _ := hex.DecodeString(plan.Checksum)
	if err := store.InsertMigration(ctx, catalog.Version+1, operationID, plan, checksum, actor, now, 0); err != nil {
		return err
	}
	version, err := store.IncrementVersion(ctx)
	if err != nil || version != catalog.Version+1 {
		return ErrConflict
	}
	return nil
}
