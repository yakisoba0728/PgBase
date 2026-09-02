package recordops

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
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

func bindRecordRelations(catalog schema.Catalog, collection schema.Collection) (*relation.Bindings, error) {
	hasRelation := false
	for _, field := range collection.Fields {
		if field.Type == schema.TypeRelation {
			hasRelation = true
			break
		}
	}
	if !hasRelation {
		return nil, nil
	}
	bindings, err := relation.Bind(catalog, collection)
	if err != nil {
		return nil, schema.ErrConflict
	}
	return bindings, nil
}

// validateRecordSize refuses a record whose encoding the list page budget
// cannot carry. The failure is a field-level validation so it surfaces as 422
// alongside every other record validation rather than as an opaque conflict.
//
// The size a PATCH produces is unknown until the row is written and read back
// with its relations attached, so this runs on the record relation.Write.Store
// returns and never on the pre-image it snapshots.
func validateRecordSize(record map[string]any) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("measure record size: %w", err)
	}
	if len(encoded) > maximumRecordBytes {
		return &query.ValidationError{
			Details: []query.ValidationDetail{{Field: "record", Code: "record_too_large"}},
		}
	}
	return nil
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
