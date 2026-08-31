package recordops

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type relationMutation struct {
	bindings *relation.Bindings
	input    relation.Input
	present  bool
	compiler map[string]json.RawMessage
}

func prepareRelationMutation(
	catalog schema.Catalog,
	collection schema.Collection,
	mode relation.InputMode,
	payload map[string]json.RawMessage,
) (*relationMutation, error) {
	bindings, err := bindRecordRelations(catalog, collection)
	if err != nil {
		return nil, err
	}
	if bindings == nil {
		return &relationMutation{compiler: cloneRecordInput(payload)}, nil
	}
	decoded, err := bindings.Decode(mode, payload)
	if err != nil {
		return nil, normalizeRelationError(err)
	}
	compiler := cloneRecordInput(decoded.Scalar)
	present := false
	for _, value := range decoded.Relations() {
		if !value.Present {
			continue
		}
		present = true
		compiler[value.FieldName] = append(json.RawMessage(nil), value.JSON...)
	}
	return &relationMutation{bindings: bindings, input: decoded, present: present, compiler: compiler}, nil
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

func (m *relationMutation) lock(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID, state relation.SourceState) error {
	if m == nil || !m.present {
		return nil
	}
	if err := m.bindings.LockCollections(ctx, tx, m.input); err != nil {
		return normalizeRelationError(err)
	}
	return normalizeRelationError(m.bindings.LockRows(ctx, tx, sourceID, state, m.input))
}

func (m *relationMutation) replaceMulti(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID) error {
	if m == nil || !m.present {
		return nil
	}
	return normalizeRelationError(m.bindings.ReplaceMulti(ctx, tx, sourceID, m.input))
}

func (m *relationMutation) hydrate(ctx context.Context, tx pgx.Tx, recordID uuid.UUID, record map[string]any) error {
	if m == nil {
		return nil
	}
	return hydrateRecordRelations(ctx, tx, m.bindings, recordID, record)
}

func readRecordWithRelations(
	ctx context.Context,
	tx pgx.Tx,
	catalog schema.Catalog,
	collection schema.Collection,
	recordID uuid.UUID,
	lock bool,
) (map[string]any, error) {
	bindings, err := bindRecordRelations(catalog, collection)
	if err != nil {
		return nil, err
	}
	record, err := readRecord(ctx, tx, collection, recordID, lock)
	if err != nil {
		return nil, err
	}
	if err := hydrateRecordRelations(ctx, tx, bindings, recordID, record); err != nil {
		return nil, err
	}
	return record, nil
}

func hydrateRecordRelations(
	ctx context.Context,
	tx pgx.Tx,
	bindings *relation.Bindings,
	recordID uuid.UUID,
	record map[string]any,
) error {
	if bindings == nil || !bindings.HasMultiple() {
		return nil
	}
	if err := bindings.Hydrate(ctx, tx, []uuid.UUID{recordID}, map[uuid.UUID]map[string]any{recordID: record}); err != nil {
		return normalizeRelationError(err)
	}
	return nil
}

func normalizeRelationError(err error) error {
	if err == nil {
		return nil
	}
	var validation *relation.ValidationError
	if errors.As(err, &validation) {
		details := make([]query.ValidationDetail, len(validation.Details))
		for index, detail := range validation.Details {
			details[index] = query.ValidationDetail{Field: detail.Field, Code: detail.Code}
		}
		return &query.ValidationError{Details: details}
	}
	if errors.Is(err, relation.ErrSourceNotFound) {
		return query.ErrRecordNotFound
	}
	if errors.Is(err, relation.ErrInvalidBinding) || errors.Is(err, relation.ErrInvalidStoredValue) {
		return schema.ErrConflict
	}
	return err
}

func cloneRecordInput(input map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		clone[name] = append(json.RawMessage(nil), raw...)
	}
	return clone
}
