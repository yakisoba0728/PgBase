package query

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const rootHydrationBatchSize = 16

type rootStub struct {
	id           uuid.UUID
	cursorValues []json.RawMessage
}
type rootHydrator struct {
	querier    relation.RowsQuerier
	bindings   *relation.Bindings
	collection schema.Collection
	bindErr    error
}

func newRootHydrator(querier relation.RowsQuerier, catalog schema.Catalog, collection schema.Collection) *rootHydrator {
	bindings, err := relation.Bind(catalog, collection)
	return &rootHydrator{querier: querier, bindings: bindings, collection: collection, bindErr: err}
}
func compactRootCandidate(candidate pageCandidate) (rootStub, error) {
	if candidate.id == uuid.Nil || candidate.root == nil {
		return rootStub{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	id, err := canonicalRecordID(candidate.root)
	if err != nil || id != candidate.id {
		return rootStub{}, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	return rootStub{id: id, cursorValues: cloneCursorValues(candidate.cursorValues)}, nil
}
func (h *rootHydrator) replaySequential(ctx context.Context, stubs []rootStub, visit func(pageCandidate) (bool, error)) (int, error) {
	if h == nil || len(stubs) == 0 {
		return 0, nil
	}
	if h.bindErr != nil {
		return 0, relationHydrationFailure(h.bindErr)
	}
	if len(stubs) > rootHydrationBatchSize || h.bindings == nil || visit == nil {
		return 0, relationHydrationFailure(relation.ErrInvalidBinding)
	}
	ids := make([]uuid.UUID, len(stubs))
	for index, stub := range stubs {
		ids[index] = stub.id
	}
	index := 0
	var visitErr error
	visited, err := h.bindings.ReplayHydratedSequential(ctx, h.querier, ids,
		func(row pgx.Row) (uuid.UUID, map[string]any, error) {
			record, decodeErr := EncodeRecord(h.collection, row)
			if decodeErr != nil {
				return uuid.Nil, nil, decodeErr
			}
			id, decodeErr := canonicalRecordID(record)
			return id, record, decodeErr
		},
		func(id uuid.UUID, record map[string]any) bool {
			candidate := pageCandidate{id: id, root: record, wire: record, cursorValues: cloneCursorValues(stubs[index].cursorValues), hydrated: true}
			index++
			var keepGoing bool
			keepGoing, visitErr = visit(candidate)
			return keepGoing && visitErr == nil
		})
	if visitErr != nil {
		return visited, visitErr
	}
	if err != nil {
		return visited, relationHydrationFailure(err)
	}
	return visited, nil
}
func (h *rootHydrator) release(candidate *pageCandidate) {
	if h == nil || candidate == nil || !candidate.hydrated {
		return
	}
	candidate.root = nil
	candidate.wire = nil
	candidate.hydrated = false
}
func (h *rootHydrator) ensurePrefix(ctx context.Context, candidates []pageCandidate, keep int) (int, error) {
	if keep < 0 || keep > len(candidates) {
		return 0, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	promoted := 0
	for start := 0; start < keep; {
		indices := make([]int, 0, rootHydrationBatchSize)
		stubs := make([]rootStub, 0, rootHydrationBatchSize)
		for index := start; index < keep && len(stubs) < rootHydrationBatchSize; index++ {
			start = index + 1
			if candidates[index].hydrated {
				continue
			}
			indices = append(indices, index)
			stubs = append(stubs, rootStub{id: candidates[index].id, cursorValues: cloneCursorValues(candidates[index].cursorValues)})
		}
		if len(stubs) == 0 {
			continue
		}
		attached := 0
		visited, err := h.replaySequential(ctx, stubs, func(candidate pageCandidate) (bool, error) {
			target := &candidates[indices[attached]]
			candidate.wireBytes = target.wireBytes
			candidate.wireMeasured = target.wireMeasured
			candidate.oversized = target.oversized
			*target = candidate
			attached++
			return true, nil
		})
		promoted += visited
		if err != nil {
			return promoted, err
		}
		if visited != len(stubs) {
			return promoted, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
	}
	return promoted, nil
}
