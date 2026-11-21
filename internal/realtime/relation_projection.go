package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordartifact"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type eventRelationProjection struct {
	record map[string]any
	field  schema.Field
	ids    []uuid.UUID
	values []string
}

// Replay keeps the event's original values, but checks each original target
// against today's policy. Public IDs cannot be used for that lookup: a deleted
// target's public ID may already belong to another record. Legacy envelopes
// without stable relation metadata therefore redact their relation values.
func filterEventRelations(ctx context.Context, tx pgx.Tx, catalog schema.Catalog, collection schema.Collection, request ReadRequest, events []Event, rawSnapshots [][]byte) error {
	if request.Principal.Kind == identity.Administrator {
		return nil
	}
	projections := make([]eventRelationProjection, 0)
	targetIDs := make(map[uuid.UUID]map[uuid.UUID]bool)
	for index, event := range events {
		var snapshots map[uuid.UUID]recordartifact.RelationSnapshot
		if len(rawSnapshots[index]) != 0 {
			if err := json.Unmarshal(rawSnapshots[index], &snapshots); err != nil {
				return errors.New("invalid realtime relation snapshot")
			}
		}
		for _, field := range collection.Fields {
			if field.Type != schema.TypeRelation || field.Relation == nil {
				continue
			}
			value, present := event.Record[field.Name]
			if !present {
				continue
			}
			snapshot, present := snapshots[field.ID]
			if !present || snapshot.TargetCollectionID != field.Relation.TargetCollectionID || snapshot.Cardinality != field.Relation.Cardinality {
				clearEventRelation(event.Record, field)
				continue
			}
			values, err := eventRelationValues(field, value, snapshot.IDs)
			if err != nil {
				return err
			}
			projections = append(projections, eventRelationProjection{record: event.Record, field: field, ids: snapshot.IDs, values: values})
			if targetIDs[snapshot.TargetCollectionID] == nil {
				targetIDs[snapshot.TargetCollectionID] = make(map[uuid.UUID]bool)
			}
			for _, id := range snapshot.IDs {
				targetIDs[snapshot.TargetCollectionID][id] = true
			}
		}
	}
	visible := make(map[uuid.UUID]map[uuid.UUID]bool)
	targets := make([]uuid.UUID, 0, len(targetIDs))
	for id := range targetIDs {
		targets = append(targets, id)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })
	for _, targetID := range targets {
		var target schema.Collection
		for _, candidate := range catalog.Collections {
			if candidate.ID == targetID {
				target = candidate
				break
			}
		}
		if target.ID == uuid.Nil {
			return schema.ErrConflict
		}
		ids := make([]uuid.UUID, 0, len(targetIDs[targetID]))
		for id := range targetIDs[targetID] {
			ids = append(ids, id)
		}
		set, err := query.VisibleRelationIDs(ctx, tx, catalog, target, request.Principal, ids, request.RuleContext)
		if errors.Is(err, recordaccess.ErrDenied) {
			continue
		}
		if err != nil {
			return err
		}
		visible[targetID] = set
	}
	for _, projection := range projections {
		set := visible[projection.field.Relation.TargetCollectionID]
		if projection.field.Relation.Cardinality == schema.RelationSingle {
			if len(projection.ids) == 0 || !set[projection.ids[0]] {
				projection.record[projection.field.Name] = nil
			}
			continue
		}
		values := make([]string, 0, len(projection.values))
		for index, id := range projection.ids {
			if set[id] {
				values = append(values, projection.values[index])
			}
		}
		projection.record[projection.field.Name] = values
	}
	return nil
}

func clearEventRelation(record map[string]any, field schema.Field) {
	if field.Relation.Cardinality == schema.RelationSingle {
		record[field.Name] = nil
	} else {
		record[field.Name] = []string{}
	}
}

func eventRelationValues(field schema.Field, value any, ids []uuid.UUID) ([]string, error) {
	invalid := errors.New("invalid realtime relation snapshot")
	var values []string
	if field.Relation.Cardinality == schema.RelationSingle {
		if value != nil {
			text, ok := value.(string)
			if !ok || text == "" {
				return nil, invalid
			}
			values = []string{text}
		}
	} else {
		switch raw := value.(type) {
		case []string:
			values = raw
		case []any:
			for _, item := range raw {
				text, ok := item.(string)
				if !ok || text == "" {
					return nil, invalid
				}
				values = append(values, text)
			}
		default:
			return nil, invalid
		}
	}
	if len(values) != len(ids) {
		return nil, invalid
	}
	for _, id := range ids {
		if id == uuid.Nil {
			return nil, invalid
		}
	}
	return values, nil
}
