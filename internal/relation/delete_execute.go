package relation

import (
	"bytes"
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// ExecuteStorage applies only relation-storage effects using the private
// transaction capability captured by Plan. No caller-supplied transaction can
// accidentally bypass the collection and row locks acquired for this plan.
// It never deletes a base record or writes updated_at, revisions, audit, or
// outbox.
func (p DeletePlan) ExecuteStorage(ctx context.Context) error {
	if p.owner == nil || p.tx == nil || len(p.deletes) == 0 {
		return ErrInvalidBinding
	}
	return p.owner.executeStorage(ctx, p.tx, p)
}

func (g *DeleteGraph) executeStorage(ctx context.Context, tx pgx.Tx, plan DeletePlan) error {
	for _, group := range groupDeleteClears(plan.clears) {
		if group.relation.spec.Cardinality == schema.RelationSingle {
			if err := executeSingleClearBatch(ctx, tx, group); err != nil {
				return err
			}
			continue
		}
		if err := executeMultiClearBatch(ctx, tx, group); err != nil {
			return err
		}
	}

	byCollection := make(map[uuid.UUID][]uuid.UUID)
	for _, record := range plan.deletes {
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
		for _, relation := range collection.fields {
			switch relation.spec.Cardinality {
			case schema.RelationMultiple:
				if _, err := tx.Exec(ctx, "delete from "+relation.storageSQL+" where source_id = any($1::uuid[])", ids); err != nil {
					return newDeleteOperationError(err)
				}
			case schema.RelationSingle:
				if relation.spec.MinSelect != 0 {
					continue
				}
				if _, err := tx.Exec(ctx, "update "+relation.source.tableSQL+" set "+relation.storageSQL+
					" = null where id = any($1::uuid[]) and "+relation.storageSQL+" is not null", ids); err != nil {
					return newDeleteOperationError(err)
				}
			default:
				return ErrInvalidBinding
			}
		}
	}
	return nil
}

type deleteClearGroup struct {
	relation deleteRelationBinding
	clears   []deleteClear
}

func groupDeleteClears(clears []deleteClear) []deleteClearGroup {
	type key struct {
		collectionID uuid.UUID
		fieldID      uuid.UUID
	}
	groups := make(map[key]*deleteClearGroup)
	keys := make([]key, 0)
	for _, clear := range clears {
		groupKey := key{collectionID: clear.relation.source.id, fieldID: clear.relation.field.ID}
		group := groups[groupKey]
		if group == nil {
			group = &deleteClearGroup{relation: clear.relation}
			groups[groupKey] = group
			keys = append(keys, groupKey)
		}
		group.clears = append(group.clears, clear)
	}
	sort.Slice(keys, func(i, j int) bool {
		if comparison := bytes.Compare(keys[i].collectionID[:], keys[j].collectionID[:]); comparison != 0 {
			return comparison < 0
		}
		left := groups[keys[i]].relation.field
		right := groups[keys[j]].relation.field
		if left.Ordinal != right.Ordinal {
			return left.Ordinal < right.Ordinal
		}
		return bytes.Compare(left.ID[:], right.ID[:]) < 0
	})
	result := make([]deleteClearGroup, 0, len(keys))
	for _, groupKey := range keys {
		result = append(result, *groups[groupKey])
	}
	return result
}

func executeSingleClearBatch(ctx context.Context, tx pgx.Tx, group deleteClearGroup) error {
	sourceIDs := make([]uuid.UUID, 0, len(group.clears))
	targetIDs := make([]uuid.UUID, 0, len(group.clears))
	for _, clear := range group.clears {
		if len(clear.targetIDs) != 1 {
			return ErrInvalidStoredValue
		}
		sourceIDs = append(sourceIDs, clear.source.RecordID)
		targetIDs = append(targetIDs, clear.targetIDs[0])
	}
	tag, err := tx.Exec(ctx, `with requested(source_id, target_id) as (
		select * from unnest($1::uuid[], $2::uuid[])
	)
	update `+group.relation.source.tableSQL+` as sources
	set `+group.relation.storageSQL+` = null
	from requested
	where sources.id = requested.source_id
	  and sources.`+group.relation.storageSQL+` = requested.target_id`, sourceIDs, targetIDs)
	if err != nil {
		return newDeleteOperationError(err)
	}
	if tag.RowsAffected() != int64(len(group.clears)) {
		return ErrInvalidStoredValue
	}
	return nil
}

func executeMultiClearBatch(ctx context.Context, tx pgx.Tx, group deleteClearGroup) error {
	sourceIDs := make([]uuid.UUID, 0, len(group.clears))
	pairSources := make([]uuid.UUID, 0)
	pairTargets := make([]uuid.UUID, 0)
	for _, clear := range group.clears {
		sourceIDs = append(sourceIDs, clear.source.RecordID)
		for _, targetID := range clear.targetIDs {
			pairSources = append(pairSources, clear.source.RecordID)
			pairTargets = append(pairTargets, targetID)
		}
	}
	tag, err := tx.Exec(ctx, `with requested(source_id, target_id) as (
		select * from unnest($1::uuid[], $2::uuid[])
	)
	delete from `+group.relation.storageSQL+` as edges
	using requested
	where edges.source_id = requested.source_id and edges.target_id = requested.target_id`, pairSources, pairTargets)
	if err != nil {
		return newDeleteOperationError(err)
	}
	if tag.RowsAffected() != int64(len(pairTargets)) {
		return ErrInvalidStoredValue
	}

	// Shift out of the live unique-position range before compacting. The
	// catalog maximum is 1000, so maximum+1 cannot collide with a valid row and
	// remains comfortably inside integer range.
	offset := group.relation.spec.MaxSelect + 1
	if _, err := tx.Exec(ctx, "update "+group.relation.storageSQL+
		" set position = position + $2 where source_id = any($1::uuid[])", sourceIDs, offset); err != nil {
		return newDeleteOperationError(err)
	}
	if _, err := tx.Exec(ctx, `with ordered as (
		select source_id, target_id,
		       (row_number() over (partition by source_id order by position) - 1)::integer as new_position
		from `+group.relation.storageSQL+` where source_id = any($1::uuid[])
	)
	update `+group.relation.storageSQL+` as edges
	set position = ordered.new_position
	from ordered
	where edges.source_id = ordered.source_id and edges.target_id = ordered.target_id`, sourceIDs); err != nil {
		return newDeleteOperationError(err)
	}
	return nil
}
