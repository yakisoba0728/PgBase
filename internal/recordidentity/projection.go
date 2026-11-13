package recordidentity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type identityKey struct{ collection, record uuid.UUID }

type projectionIdentity struct {
	key     identityKey
	literal string
	text    bool
}

type projectionChange struct {
	record      map[string]any
	field       string
	values      []projectionIdentity
	scalar      bool
	stringSlice bool
}

type identityProjection struct {
	collections map[uuid.UUID]schema.Collection
	identities  map[identityKey]string
	changes     []projectionChange
}

// Project resolves the IDs of hydrated records before changing any response.
// Captured assignments also allow the same expanded map to appear more than once.
func (s *Service) Project(ctx context.Context, db DB, catalog schema.Catalog, collection schema.Collection, records ...map[string]any) error {
	plan := identityProjection{collections: make(map[uuid.UUID]schema.Collection, len(catalog.Collections)), identities: make(map[identityKey]string)}
	for _, target := range catalog.Collections {
		plan.collections[target.ID] = target
	}
	for _, record := range records {
		if err := plan.collectRecord(collection, record); err != nil {
			return err
		}
	}
	if err := plan.resolve(ctx, db); err != nil {
		return err
	}
	value := func(id projectionIdentity) string {
		if id.text {
			return plan.identities[id.key]
		}
		return id.literal
	}
	for _, change := range plan.changes {
		switch {
		case change.scalar:
			change.record[change.field] = value(change.values[0])
		case change.stringSlice:
			values := make([]string, len(change.values))
			for i, id := range change.values {
				values[i] = value(id)
			}
			change.record[change.field] = values
		default:
			values := make([]any, len(change.values))
			for i, id := range change.values {
				values[i] = value(id)
			}
			change.record[change.field] = values
		}
	}
	return nil
}

func (p *identityProjection) identity(collection schema.Collection, id uuid.UUID) projectionIdentity {
	if collection.RecordID.Kind != schema.RecordIDText {
		return projectionIdentity{literal: id.String()}
	}
	key := identityKey{identityCollectionID(collection), id}
	p.identities[key] = ""
	return projectionIdentity{key: key, text: true}
}

func (p *identityProjection) collectRecord(collection schema.Collection, record map[string]any) error {
	if record == nil {
		return nil
	}
	if id, ok := stringUUID(record["id"]); ok {
		p.changes = append(p.changes, projectionChange{record: record, field: "id", scalar: true, values: []projectionIdentity{p.identity(collection, id)}})
	}
	for _, field := range collection.Fields {
		if field.Relation == nil {
			continue
		}
		target, ok := p.collections[field.Relation.TargetCollectionID]
		if !ok {
			return fmt.Errorf("project relation %s: missing target collection", field.Name)
		}
		if value, present := record[field.Name]; present {
			if err := p.collectRelation(target, record, field.Name, value); err != nil {
				return err
			}
		}
		if expands, ok := record["expand"].(map[string]any); ok {
			switch expanded := expands[field.Name].(type) {
			case map[string]any:
				if err := p.collectRecord(target, expanded); err != nil {
					return err
				}
			case []map[string]any:
				for _, child := range expanded {
					if err := p.collectRecord(target, child); err != nil {
						return err
					}
				}
			case []any:
				for _, raw := range expanded {
					if child, ok := raw.(map[string]any); ok {
						if err := p.collectRecord(target, child); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func (p *identityProjection) collectRelation(target schema.Collection, record map[string]any, field string, value any) error {
	change := projectionChange{record: record, field: field}
	switch ids := value.(type) {
	case string:
		id, ok := stringUUID(ids)
		if !ok {
			return nil
		}
		change.scalar, change.values = true, []projectionIdentity{p.identity(target, id)}
	case []string:
		change.stringSlice = true
		change.values = make([]projectionIdentity, len(ids))
		for i, raw := range ids {
			id, ok := stringUUID(raw)
			if !ok {
				return ErrInvalid
			}
			change.values[i] = p.identity(target, id)
		}
	case []any:
		change.values = make([]projectionIdentity, len(ids))
		for i, raw := range ids {
			text, ok := raw.(string)
			if !ok {
				return ErrInvalid
			}
			if id, ok := stringUUID(text); ok {
				change.values[i] = p.identity(target, id)
			} else {
				change.values[i] = projectionIdentity{literal: text}
			}
		}
	default:
		return nil
	}
	p.changes = append(p.changes, change)
	return nil
}

func (p *identityProjection) resolve(ctx context.Context, db DB) error {
	if len(p.identities) == 0 {
		return nil
	}
	collections, ids := make([]uuid.UUID, 0, len(p.identities)), make([]uuid.UUID, 0, len(p.identities))
	for key := range p.identities {
		collections, ids = append(collections, key.collection), append(ids, key.record)
	}
	rows, err := db.Query(ctx, `SELECT identity.collection_id, identity.internal_id, identity.external_id
		FROM unnest($1::uuid[], $2::uuid[]) AS input(collection_id, internal_id)
		JOIN pgbase_meta.record_identities identity
		  ON identity.collection_kind='base' AND identity.collection_id=input.collection_id AND identity.internal_id=input.internal_id`, collections, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key identityKey
		var public string
		if err := rows.Scan(&key.collection, &key.record, &public); err != nil {
			return err
		}
		p.identities[key] = public
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, public := range p.identities {
		if public == "" {
			return ErrNotFound
		}
	}
	return nil
}

func stringUUID(value any) (uuid.UUID, bool) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) != text {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(text)
	return id, err == nil && id != uuid.Nil
}
