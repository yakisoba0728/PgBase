package relation

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Write is the relation half of one record mutation. It owns the order in
// which collection advisory locks, row locks, edge replacement and hydration
// run so no caller can assemble a different one, and it is valid for exactly
// one transaction attempt.
type Write struct {
	bindings *Bindings
	input    writeInput
	mode     WriteMode
	present  bool
	scalar   map[string]json.RawMessage
	tx       pgx.Tx
	locked   bool
	stored   bool
}

// PrepareWrite decodes payload against collection's relation fields. A
// collection that declares no relation field skips catalog binding entirely,
// so an ordinary record write never pays to validate the whole catalog.
func PrepareWrite(
	catalog schema.Catalog,
	collection schema.Collection,
	mode WriteMode,
	payload map[string]json.RawMessage,
) (*Write, error) {
	if mode != CreateWrite && mode != PatchWrite {
		return nil, ErrInvalidBinding
	}
	if !declaresRelation(collection) {
		return &Write{mode: mode, scalar: cloneRawJSON(payload)}, nil
	}
	bindings, err := Bind(catalog, collection)
	if err != nil {
		return nil, err
	}
	decoded, err := bindings.decode(mode, payload)
	if err != nil {
		return nil, err
	}
	write := &Write{bindings: bindings, input: decoded, mode: mode, scalar: cloneRawJSON(decoded.Scalar)}
	for _, value := range decoded.relations {
		if !value.Present {
			continue
		}
		write.present = true
		write.scalar[value.FieldName] = append(json.RawMessage(nil), value.JSON...)
	}
	return write, nil
}

// Scalar is the compiler input: cloned non-relation members plus every present
// relation value in canonical form.
func (w *Write) Scalar() map[string]json.RawMessage {
	if w == nil {
		return nil
	}
	return w.scalar
}

// LocksSource reports whether Lock already took the stored source row FOR
// UPDATE, so a caller's own pre-image read does not repeat that lock.
func (w *Write) LocksSource() bool { return w != nil && w.present }

// Lock takes every changed collection's advisory lock in UUID order and then
// the source and target row locks in (collection, record) order. A request
// that touches no relation field takes neither.
func (w *Write) Lock(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID) error {
	if w == nil || tx == nil || w.locked {
		return ErrInvalidBinding
	}
	if !w.present {
		w.tx, w.locked = tx, true
		return nil
	}
	if err := w.bindings.lockCollections(ctx, tx, w.input); err != nil {
		return err
	}
	state := sourceStored
	if w.mode == CreateWrite {
		state = sourcePending
	}
	if err := w.bindings.lockRows(ctx, tx, sourceID, state, w.input); err != nil {
		return err
	}
	w.tx, w.locked = tx, true
	return nil
}

// Snapshot hydrates a stored pre-image's multiple-relation fields. It is
// deliberately unbounded: a record an older build stored above the record size
// bound stays writable, so an operator can shrink it with a PATCH instead of
// finding it both unreadable through list and frozen.
func (w *Write) Snapshot(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID, record map[string]any) error {
	if w == nil || !w.locked || tx == nil || tx != w.tx {
		return ErrInvalidBinding
	}
	return w.bindings.hydrateRecord(ctx, tx, sourceID, record)
}

// Store replaces every present multiple relation and hydrates the record the
// base write returned. The caller must have written the base row first: edges
// reference it, and the hydrated result is the only point either write path
// holds the merged record the request produced.
func (w *Write) Store(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID, record map[string]any) error {
	if w == nil || !w.locked || w.stored || tx == nil || tx != w.tx {
		return ErrInvalidBinding
	}
	if w.present {
		if err := w.bindings.replaceMulti(ctx, tx, sourceID, w.input); err != nil {
			return err
		}
	}
	if err := w.bindings.hydrateRecord(ctx, tx, sourceID, record); err != nil {
		return err
	}
	w.stored = true
	return nil
}

func (b *Bindings) hydrateRecord(ctx context.Context, querier RowsQuerier, recordID uuid.UUID, record map[string]any) error {
	if !b.HasMultiple() {
		return nil
	}
	return b.Hydrate(ctx, querier, []uuid.UUID{recordID}, map[uuid.UUID]map[string]any{recordID: record})
}

func declaresRelation(collection schema.Collection) bool {
	return slices.ContainsFunc(collection.Fields, func(field schema.Field) bool { return field.Type == schema.TypeRelation })
}

func cloneRawJSON(input map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(input))
	for name, raw := range input {
		clone[name] = append(json.RawMessage(nil), raw...)
	}
	return clone
}
