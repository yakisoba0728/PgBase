package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	// mode and hydrations carry the record-size bound; see hydrate.
	mode       relation.InputMode
	hydrations int
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
		return &relationMutation{compiler: cloneRecordInput(payload), mode: mode}, nil
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
	return &relationMutation{
		bindings: bindings, input: decoded, present: present, compiler: compiler, mode: mode,
	}, nil
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

// hydrate fills the record's multiple-relation fields and then bounds the
// finished record. This is the only point either write path holds the merged
// result: the compiled statement carries just the fields the request supplied,
// so the size a PATCH produces is unknown until the row is written and read
// back with its relations attached.
//
// Create hydrates once, on the inserted record. Update hydrates twice, first
// on the stored pre-image and then on the updated record, and only the second
// call is bounded. Skipping the pre-image is deliberate: a record an older
// build stored above the bound stays writable, so an operator can shrink it
// with a PATCH instead of finding it both unreadable through list and frozen.
// The call order in Service.Create and Service.Update is load-bearing here and
// is pinned by TestRecordSizeBoundSkipsOnlyThePatchPreImage.
func (m *relationMutation) hydrate(ctx context.Context, tx pgx.Tx, recordID uuid.UUID, record map[string]any) error {
	if m == nil {
		return nil
	}
	m.hydrations++
	if err := hydrateRecordRelations(ctx, tx, m.bindings, recordID, record); err != nil {
		return err
	}
	if m.mode == relation.PatchInput && m.hydrations < 2 {
		return nil
	}
	return validateRecordSize(record)
}

// validateRecordSize refuses a record whose encoding the list page budget
// cannot carry. The failure is a field-level validation so it surfaces as 422
// alongside every other record validation rather than as an opaque conflict.
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

func cloneRecordInput(input map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		clone[name] = append(json.RawMessage(nil), raw...)
	}
	return clone
}
