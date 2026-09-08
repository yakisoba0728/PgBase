package recordops

import (
	"encoding/json"
	"errors"
	"slices"

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
