package schema

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func validateDocumentAuthRecordIDs(ctx context.Context, tx pgx.Tx, catalog Catalog, values []SchemaDocumentAuthCollection) error {
	for _, value := range values {
		var current []byte
		err := tx.QueryRow(ctx, `select record_id from pgbase_meta.auth_collections where name=$1`, value.Name).Scan(&current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := documentAuthRecordID(catalog, value, current); err != nil {
			return err
		}
	}
	return nil
}

// Auth collections may stand alone or accompany a physical auth collection.
// Both document entries must agree when paired; omission preserves an existing
// authority's immutable policy and inherits the catalog policy for a new one.
func documentAuthRecordID(catalog Catalog, value SchemaDocumentAuthCollection, currentRaw []byte) (RecordIDPolicy, error) {
	path := "authCollections." + value.Name + ".recordId"
	requested, err := NormalizeRecordIDPolicy(value.RecordID)
	if err != nil {
		return RecordIDPolicy{}, validation(path, "invalid")
	}
	var current RecordIDPolicy
	if currentRaw != nil {
		if json.Unmarshal(currentRaw, &current) != nil {
			return RecordIDPolicy{}, ErrCatalogInvalid
		}
		normalized, err := NormalizeRecordIDPolicy(&current)
		if err != nil || normalized != current {
			return RecordIDPolicy{}, ErrCatalogInvalid
		}
		if value.RecordID == nil {
			requested = current
		}
	}
	if collection, ok := catalog.Collections[value.Name]; ok && collection.Type == CollectionTypeAuth {
		if value.RecordID != nil && requested != collection.RecordID {
			return RecordIDPolicy{}, validation(path, "conflict")
		}
		requested = collection.RecordID
	}
	if currentRaw != nil && requested != current {
		return RecordIDPolicy{}, validation(path, "immutable")
	}
	return requested, nil
}
