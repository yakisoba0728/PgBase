package query

import (
	"context"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/relation"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const rootHydrationBatchSize = 16

type rootHydrator struct {
	querier        relation.RowsQuerier
	bindings       *relation.Bindings
	multipleFields []string
	bindErr        error
}

func newRootHydrator(
	querier relation.RowsQuerier,
	catalog schema.Catalog,
	collection schema.Collection,
) *rootHydrator {
	bindings, err := relation.Bind(catalog, collection)
	fields := make([]string, 0)
	for _, field := range collection.Fields {
		if isMultipleRelation(field) {
			fields = append(fields, field.Name)
		}
	}
	return &rootHydrator{querier: querier, bindings: bindings, multipleFields: fields, bindErr: err}
}

func (h *rootHydrator) hydrateSequential(
	ctx context.Context,
	candidates []pageCandidate,
	visit func(*pageCandidate) (bool, error),
) (int, error) {
	if h == nil || len(candidates) == 0 {
		return 0, nil
	}
	if h.bindErr != nil {
		return 0, relationHydrationFailure(h.bindErr)
	}
	if h.bindings == nil {
		return 0, relationHydrationFailure(relation.ErrInvalidBinding)
	}
	var err error
	ids := make([]uuid.UUID, len(candidates))
	for index := range candidates {
		ids[index], err = canonicalRecordID(candidates[index].root)
		if err != nil {
			return 0, relationHydrationFailure(relation.ErrInvalidStoredValue)
		}
	}
	index := 0
	var visitErr error
	visited, err := h.bindings.HydrateSequential(ctx, h.querier, ids, func(id uuid.UUID, values map[string]any) bool {
		if index >= len(candidates) || id != ids[index] {
			visitErr = relationHydrationFailure(relation.ErrInvalidStoredValue)
			return false
		}
		candidate := &candidates[index]
		for name, value := range values {
			candidate.root[name] = value
		}
		candidate.hydrated = true
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
	for _, name := range h.multipleFields {
		delete(candidate.root, name)
	}
	candidate.hydrated = false
}

func (h *rootHydrator) ensurePrefix(
	ctx context.Context,
	candidates []pageCandidate,
	keep int,
) (int, error) {
	if keep < 0 || keep > len(candidates) {
		return 0, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	pending := make([]pageCandidate, 0)
	indices := make([]int, 0)
	for index := range keep {
		if candidates[index].hydrated {
			continue
		}
		pending = append(pending, candidates[index])
		indices = append(indices, index)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	visited, err := h.hydrateSequential(ctx, pending, func(*pageCandidate) (bool, error) {
		return true, nil
	})
	if err != nil {
		return visited, err
	}
	if visited != len(pending) {
		return visited, relationHydrationFailure(relation.ErrInvalidStoredValue)
	}
	for _, index := range indices {
		candidates[index].hydrated = true
	}
	return visited, nil
}
