package relation

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	deleteMaximumDepth   = 32
	deleteMaximumRecords = 10_000
	deleteMaximumEdges   = 100_000
	deleteMaximumScans   = 100_000
)

// ErrDeleteIsolation prevents a snapshot established before the advisory lock
// from omitting a relation write that committed while the delete waited.
var ErrDeleteIsolation = errors.New("relation delete requires read committed transaction")

type deleteLimits struct {
	depth   int
	records int
	edges   int
}

var productionDeleteLimits = deleteLimits{
	depth: deleteMaximumDepth, records: deleteMaximumRecords, edges: deleteMaximumEdges,
}

// RecordRef identifies a record without relying on a mutable collection name.
type RecordRef struct {
	CollectionID uuid.UUID
	RecordID     uuid.UUID
}

// Update is the record-level projection of one or more set-null clears. It
// lets the caller emit exactly one revision/audit/outbox change per surviving
// record.
type Update struct {
	Source        RecordRef
	ChangedFields []string
}

// DeletePlan is an immutable deterministic delete closure. Callers use
// Deletes and Updates to write audit/revision/outbox and base-record changes;
// ExecuteStorage uses the private bindings to clean relation storage.
type DeletePlan struct {
	owner   *DeleteGraph
	tx      pgx.Tx
	deletes []RecordRef
	clears  []deleteClear
}

// Deletes returns a sorted caller-owned copy of the cascade closure.
func (p DeletePlan) Deletes() []RecordRef { return append([]RecordRef(nil), p.deletes...) }

// Updates groups the set-null clears by source record with sorted unique
// field names, in the same (collection, record) order as Deletes.
func (p DeletePlan) Updates() []Update {
	updates := make([]Update, 0)
	for _, clear := range p.clears {
		if len(updates) == 0 || updates[len(updates)-1].Source != clear.source {
			updates = append(updates, Update{Source: clear.source})
		}
		current := &updates[len(updates)-1]
		name := clear.relation.field.Name
		if len(current.ChangedFields) == 0 || current.ChangedFields[len(current.ChangedFields)-1] != name {
			current.ChangedFields = append(current.ChangedFields, name)
		}
	}
	for index := range updates {
		sort.Strings(updates[index].ChangedFields)
		updates[index].ChangedFields = append([]string(nil), updates[index].ChangedFields...)
	}
	return updates
}

type deleteRelationBinding struct {
	source     collectionBinding
	field      schema.Field
	spec       schema.RelationSpec
	storageSQL string
}

type deleteCollection struct {
	binding collectionBinding
	fields  []deleteRelationBinding
}

type deleteRelationKey struct {
	collectionID uuid.UUID
	fieldID      uuid.UUID
}

// DeleteGraph is a catalog-pinned relation delete planner and storage
// executor. Construct a new value whenever the schema catalog version changes.
type DeleteGraph struct {
	catalogVersion int64
	collections    map[uuid.UUID]deleteCollection
	incoming       map[uuid.UUID][]deleteRelationBinding
	limits         deleteLimits
}

// BindDeleteGraph validates the catalog's precomputed Incoming index against
// every outgoing relation field and binds all physical identifiers.
func BindDeleteGraph(catalog schema.Catalog) (*DeleteGraph, error) {
	graph := &DeleteGraph{
		catalogVersion: catalog.Version,
		collections:    make(map[uuid.UUID]deleteCollection, len(catalog.Collections)),
		incoming:       make(map[uuid.UUID][]deleteRelationBinding, len(catalog.Incoming)),
		limits:         productionDeleteLimits,
	}

	names := make([]string, 0, len(catalog.Collections))
	for name := range catalog.Collections {
		names = append(names, name)
	}
	sort.Strings(names)
	outgoing := make(map[deleteRelationKey]deleteRelationBinding)
	for _, name := range names {
		collection := catalog.Collections[name]
		bindings, err := Bind(catalog, collection)
		if err != nil {
			return nil, ErrInvalidBinding
		}
		boundCollection := deleteCollection{binding: bindings.source}
		for _, field := range bindings.fields {
			relation := deleteRelationBinding{
				source: bindings.source, field: cloneField(field.field), spec: field.spec, storageSQL: field.storageSQL,
			}
			key := deleteRelationKey{collectionID: collection.ID, fieldID: field.field.ID}
			if _, duplicate := outgoing[key]; duplicate {
				return nil, ErrInvalidBinding
			}
			outgoing[key] = relation
			boundCollection.fields = append(boundCollection.fields, relation)
		}
		graph.collections[collection.ID] = boundCollection
	}

	seen := make(map[deleteRelationKey]struct{}, len(outgoing))
	for targetID, refs := range catalog.Incoming {
		if _, ok := graph.collections[targetID]; !ok {
			return nil, ErrInvalidBinding
		}
		for _, ref := range refs {
			key := deleteRelationKey{collectionID: ref.SourceCollectionID, fieldID: ref.SourceFieldID}
			bound, ok := outgoing[key]
			if !ok || bound.spec.TargetCollectionID != targetID || bound.spec != ref.Spec {
				return nil, ErrInvalidBinding
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, ErrInvalidBinding
			}
			seen[key] = struct{}{}
			graph.incoming[targetID] = append(graph.incoming[targetID], bound)
		}
	}
	if len(seen) != len(outgoing) {
		return nil, ErrInvalidBinding
	}
	for targetID := range graph.incoming {
		sort.Slice(graph.incoming[targetID], func(i, j int) bool {
			left := graph.incoming[targetID][i]
			right := graph.incoming[targetID][j]
			if comparison := bytes.Compare(left.source.id[:], right.source.id[:]); comparison != 0 {
				return comparison < 0
			}
			if left.field.Ordinal != right.field.Ordinal {
				return left.field.Ordinal < right.field.Ordinal
			}
			return bytes.Compare(left.field.ID[:], right.field.ID[:]) < 0
		})
	}
	return graph, nil
}

// Plan acquires the complete static collection lock set before any reverse
// read, discovers the bounded record closure, rejects surviving restrict
// references, and locks every delete/clear record FOR UPDATE in global order.
// The returned plan is valid only in this transaction and with this graph.
func (g *DeleteGraph) Plan(ctx context.Context, tx pgx.Tx, root RecordRef) (DeletePlan, error) {
	if g == nil || tx == nil || root.CollectionID == uuid.Nil || root.RecordID == uuid.Nil {
		return DeletePlan{}, ErrInvalidBinding
	}
	if _, ok := g.collections[root.CollectionID]; !ok || !validDeleteLimits(g.limits) {
		return DeletePlan{}, ErrInvalidBinding
	}
	var isolation string
	if err := tx.QueryRow(ctx, "show transaction_isolation").Scan(&isolation); err != nil {
		return DeletePlan{}, newDeleteOperationError(err)
	}
	if isolation != "read committed" {
		return DeletePlan{}, ErrDeleteIsolation
	}

	participants := g.participatingCollections(root.CollectionID)
	for _, collectionID := range participants {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", collectionAdvisoryKey(collectionID)); err != nil {
			return DeletePlan{}, newDeleteOperationError(err)
		}
	}

	deletes, hits, err := g.discover(ctx, tx, root)
	if err != nil {
		return DeletePlan{}, err
	}
	deleted := make(map[RecordRef]struct{}, len(deletes))
	for _, record := range deletes {
		deleted[record] = struct{}{}
	}
	for _, hit := range hits {
		if hit.relation.spec.OnDelete != schema.RelationRestrict {
			continue
		}
		if _, inside := deleted[hit.source]; !inside {
			return DeletePlan{}, deleteValidation("relation_delete_restricted")
		}
	}

	clears, err := makeDeleteClears(hits, deleted)
	if err != nil {
		return DeletePlan{}, err
	}
	affected := make(map[RecordRef]struct{}, len(deletes)+len(clears))
	for _, record := range deletes {
		affected[record] = struct{}{}
	}
	for _, clear := range clears {
		affected[clear.source] = struct{}{}
	}
	if len(affected) > g.limits.records {
		return DeletePlan{}, deleteValidation("relation_delete_budget_exceeded")
	}
	if err := g.lockAffectedRows(ctx, tx, root, deletes, clears); err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{owner: g, tx: tx, deletes: deletes, clears: clears}, nil
}

func validDeleteLimits(limits deleteLimits) bool {
	return limits.depth >= 0 && limits.records > 0 && limits.edges > 0 &&
		limits.depth <= deleteMaximumDepth && limits.records <= deleteMaximumRecords && limits.edges <= deleteMaximumEdges
}

func (g *DeleteGraph) participatingCollections(root uuid.UUID) []uuid.UUID {
	participants := map[uuid.UUID]struct{}{root: {}}
	cascadeTargets := map[uuid.UUID]struct{}{root: {}}
	queue := []uuid.UUID{root}
	for len(queue) != 0 {
		target := queue[0]
		queue = queue[1:]
		for _, relation := range g.incoming[target] {
			participants[relation.source.id] = struct{}{}
			if relation.spec.OnDelete != schema.RelationCascade {
				continue
			}
			if _, seen := cascadeTargets[relation.source.id]; seen {
				continue
			}
			cascadeTargets[relation.source.id] = struct{}{}
			queue = append(queue, relation.source.id)
		}
	}
	ids := make([]uuid.UUID, 0, len(participants))
	for id := range participants {
		ids = append(ids, id)
	}
	sortUUIDs(ids)
	return ids
}

type deleteHit struct {
	relation deleteRelationBinding
	source   RecordRef
	target   RecordRef
}

func (g *DeleteGraph) discover(ctx context.Context, tx pgx.Tx, root RecordRef) ([]RecordRef, []deleteHit, error) {
	visited := map[RecordRef]int{root: 0}
	frontier := []RecordRef{root}
	hits := make([]deleteHit, 0)
	edgeCount := 0
	scanCount := 0
	for len(frontier) != 0 {
		byCollection := make(map[uuid.UUID][]uuid.UUID)
		depth := visited[frontier[0]]
		for _, record := range frontier {
			if visited[record] != depth {
				return nil, nil, ErrInvalidStoredValue
			}
			byCollection[record.CollectionID] = append(byCollection[record.CollectionID], record.RecordID)
		}
		collectionIDs := make([]uuid.UUID, 0, len(byCollection))
		for collectionID := range byCollection {
			collectionIDs = append(collectionIDs, collectionID)
		}
		sortUUIDs(collectionIDs)
		nextSet := make(map[RecordRef]struct{})
		for _, targetCollectionID := range collectionIDs {
			targetIDs := uniqueSortedUUIDs(byCollection[targetCollectionID])
			for _, relation := range g.incoming[targetCollectionID] {
				scanCount++
				if scanCount > deleteMaximumScans {
					return nil, nil, deleteValidation("relation_delete_budget_exceeded")
				}
				remaining := g.limits.edges - edgeCount
				relationHits, err := readDeleteHits(ctx, tx, relation, targetCollectionID, targetIDs, remaining+1)
				if err != nil {
					return nil, nil, err
				}
				for _, hit := range relationHits {
					edgeCount++
					if edgeCount > g.limits.edges {
						return nil, nil, deleteValidation("relation_delete_budget_exceeded")
					}
					hits = append(hits, hit)
					if relation.spec.OnDelete != schema.RelationCascade {
						continue
					}
					if _, seen := visited[hit.source]; seen {
						continue
					}
					nextDepth := depth + 1
					if nextDepth > g.limits.depth || len(visited) >= g.limits.records {
						return nil, nil, deleteValidation("relation_delete_budget_exceeded")
					}
					visited[hit.source] = nextDepth
					nextSet[hit.source] = struct{}{}
				}
			}
		}
		frontier = frontier[:0]
		for record := range nextSet {
			frontier = append(frontier, record)
		}
		sortRecordRefs(frontier)
	}
	deletes := make([]RecordRef, 0, len(visited))
	for record := range visited {
		deletes = append(deletes, record)
	}
	sortRecordRefs(deletes)
	return deletes, hits, nil
}

func readDeleteHits(
	ctx context.Context,
	tx pgx.Tx,
	relation deleteRelationBinding,
	targetCollectionID uuid.UUID,
	targetIDs []uuid.UUID,
	limit int,
) ([]deleteHit, error) {
	statement := "select id, " + relation.storageSQL + " from " + relation.source.tableSQL +
		" where " + relation.storageSQL + " = any($1::uuid[]) order by " + relation.storageSQL + ", id limit $2"
	if relation.spec.Cardinality == schema.RelationMultiple {
		statement = "select source_id, target_id from " + relation.storageSQL +
			" where target_id = any($1::uuid[]) order by target_id, source_id limit $2"
	}
	rows, err := tx.Query(ctx, statement, targetIDs, limit)
	if err != nil {
		return nil, newDeleteOperationError(err)
	}
	requested := make(map[uuid.UUID]struct{}, len(targetIDs))
	for _, id := range targetIDs {
		requested[id] = struct{}{}
	}
	seen := make(map[[2]uuid.UUID]struct{})
	hits := make([]deleteHit, 0)
	for rows.Next() {
		var sourceID, targetID uuid.UUID
		if err := rows.Scan(&sourceID, &targetID); err != nil {
			rows.Close()
			return nil, newDeleteOperationError(err)
		}
		pair := [2]uuid.UUID{sourceID, targetID}
		if sourceID == uuid.Nil || targetID == uuid.Nil {
			rows.Close()
			return nil, ErrInvalidStoredValue
		}
		if _, ok := requested[targetID]; !ok {
			rows.Close()
			return nil, ErrInvalidStoredValue
		}
		if _, duplicate := seen[pair]; duplicate {
			rows.Close()
			return nil, ErrInvalidStoredValue
		}
		seen[pair] = struct{}{}
		hits = append(hits, deleteHit{
			relation: relation,
			source:   RecordRef{CollectionID: relation.source.id, RecordID: sourceID},
			target:   RecordRef{CollectionID: targetCollectionID, RecordID: targetID},
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, newDeleteOperationError(err)
	}
	rows.Close()
	return hits, nil
}

type deleteClearKey struct {
	source  RecordRef
	fieldID uuid.UUID
}

// deleteClear is one surviving source field whose references into the delete
// closure must be removed. targetIDs are sorted and never appear in an error
// string.
type deleteClear struct {
	source    RecordRef
	targetIDs []uuid.UUID
	relation  deleteRelationBinding
}

func makeDeleteClears(hits []deleteHit, deleted map[RecordRef]struct{}) ([]deleteClear, error) {
	clears := make(map[deleteClearKey]*deleteClear)
	targetSets := make(map[deleteClearKey]map[uuid.UUID]struct{})
	for _, hit := range hits {
		if hit.relation.spec.OnDelete != schema.RelationSetNull {
			continue
		}
		if _, inside := deleted[hit.source]; inside {
			continue
		}
		key := deleteClearKey{source: hit.source, fieldID: hit.relation.field.ID}
		clear := clears[key]
		if clear == nil {
			clear = &deleteClear{source: hit.source, relation: hit.relation}
			clears[key] = clear
			targetSets[key] = make(map[uuid.UUID]struct{})
		}
		targetSets[key][hit.target.RecordID] = struct{}{}
	}

	keys := make([]deleteClearKey, 0, len(clears))
	for key := range clears {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if recordRefLess(keys[i].source, keys[j].source) {
			return true
		}
		if recordRefLess(keys[j].source, keys[i].source) {
			return false
		}
		left := clears[keys[i]].relation.field
		right := clears[keys[j]].relation.field
		if left.Ordinal != right.Ordinal {
			return left.Ordinal < right.Ordinal
		}
		return bytes.Compare(keys[i].fieldID[:], keys[j].fieldID[:]) < 0
	})
	ordered := make([]deleteClear, 0, len(keys))
	for _, key := range keys {
		clear := clears[key]
		for targetID := range targetSets[key] {
			clear.targetIDs = append(clear.targetIDs, targetID)
		}
		sortUUIDs(clear.targetIDs)
		if clear.relation.spec.Cardinality == schema.RelationSingle && len(clear.targetIDs) != 1 {
			return nil, ErrInvalidStoredValue
		}
		ordered = append(ordered, *clear)
	}
	return ordered, nil
}

func (g *DeleteGraph) lockAffectedRows(
	ctx context.Context,
	tx pgx.Tx,
	root RecordRef,
	deletes []RecordRef,
	clears []deleteClear,
) error {
	affected := make(map[RecordRef]struct{}, len(deletes)+len(clears))
	for _, record := range deletes {
		affected[record] = struct{}{}
	}
	for _, clear := range clears {
		affected[clear.source] = struct{}{}
	}
	byCollection := make(map[uuid.UUID][]uuid.UUID)
	for record := range affected {
		byCollection[record.CollectionID] = append(byCollection[record.CollectionID], record.RecordID)
	}
	collectionIDs := make([]uuid.UUID, 0, len(byCollection))
	for id := range byCollection {
		collectionIDs = append(collectionIDs, id)
	}
	sortUUIDs(collectionIDs)
	for _, collectionID := range collectionIDs {
		collection, ok := g.collections[collectionID]
		if !ok {
			return ErrInvalidBinding
		}
		ids := uniqueSortedUUIDs(byCollection[collectionID])
		rows, err := tx.Query(ctx, "select id from "+collection.binding.tableSQL+
			" where id = any($1::uuid[]) order by id FOR UPDATE", ids)
		if err != nil {
			return newDeleteOperationError(err)
		}
		found := make(map[uuid.UUID]struct{}, len(ids))
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return newDeleteOperationError(err)
			}
			found[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return newDeleteOperationError(err)
		}
		rows.Close()
		if collectionID == root.CollectionID {
			if _, ok := found[root.RecordID]; !ok {
				return ErrSourceNotFound
			}
		}
		for _, id := range ids {
			if _, ok := found[id]; ok {
				continue
			}
			return ErrInvalidStoredValue
		}
	}
	return nil
}

func sortRecordRefs(records []RecordRef) {
	sort.Slice(records, func(i, j int) bool { return recordRefLess(records[i], records[j]) })
}

func recordRefLess(left, right RecordRef) bool {
	if comparison := bytes.Compare(left.CollectionID[:], right.CollectionID[:]); comparison != 0 {
		return comparison < 0
	}
	return bytes.Compare(left.RecordID[:], right.RecordID[:]) < 0
}

func deleteValidation(code string) error { return validation("id", code) }

type deleteOperationError struct{ cause error }

func newDeleteOperationError(cause error) error {
	if cause == nil {
		return nil
	}
	return &deleteOperationError{cause: cause}
}

func (e *deleteOperationError) Error() string { return "relation delete operation failed" }
func (e *deleteOperationError) Unwrap() error { return e.cause }
