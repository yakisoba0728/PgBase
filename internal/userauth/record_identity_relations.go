package userauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type relationIdentityTarget struct {
	id     uuid.UUID
	kind   string
	policy schema.RecordIDPolicy
}

func loadRelationIdentityTarget(ctx context.Context, db recordidentity.DB, name string) (relationIdentityTarget, error) {
	var target relationIdentityTarget
	var raw []byte
	err := db.QueryRow(ctx, `with targets as (
		select id,'auth'::text kind,record_id from pgbase_meta.auth_collections where name=$1
		union all select id,'base'::text,record_id from pgbase_meta.collections where name=$1 and collection_type<>'auth'
	) select id,kind,record_id from targets where (select count(*) from targets)=1`, name).Scan(&target.id, &target.kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, ErrValidation
	}
	if err != nil {
		return target, err
	}
	if err := json.Unmarshal(raw, &target.policy); err != nil {
		return target, ErrValidation
	}
	return target, nil
}

func (s *Service) resolveRelationPublicID(ctx context.Context, db recordidentity.DB, target relationIdentityTarget, public string) (uuid.UUID, error) {
	if target.kind == "auth" {
		return s.recordIdentities.ResolveAuth(ctx, db, target.id, target.policy, public)
	}
	return s.recordIdentities.Resolve(ctx, db, schema.Collection{ID: target.id, RecordID: target.policy}, public)
}

func (s *Service) resolveAuthRelationIdentities(ctx context.Context, db recordidentity.DB, attributes map[string]any, fields []schema.FieldInput) error {
	for _, field := range fields {
		if field.Type != schema.TypeRelation || attributes[field.Name] == nil {
			continue
		}
		target, err := loadRelationIdentityTarget(ctx, db, field.Relation.TargetCollection)
		if err != nil {
			return err
		}
		if field.Relation.Cardinality == schema.RelationMultiple {
			values, ok := attributes[field.Name].([]any)
			if !ok {
				return ErrValidation
			}
			for i, raw := range values {
				public, ok := raw.(string)
				if !ok {
					return ErrValidation
				}
				id, err := s.resolveRelationPublicID(ctx, db, target, public)
				if err != nil {
					return ErrValidation
				}
				values[i] = id.String()
			}
			attributes[field.Name] = values
			continue
		}
		public, ok := attributes[field.Name].(string)
		if !ok {
			return ErrValidation
		}
		id, err := s.resolveRelationPublicID(ctx, db, target, public)
		if err != nil {
			return ErrValidation
		}
		attributes[field.Name] = id.String()
	}
	return nil
}

func (s *Service) projectAuthRelationIdentities(ctx context.Context, db recordidentity.DB, attributes map[string]any, fields []schema.FieldInput) error {
	return s.projectAuthRelationDocuments(ctx, db, []map[string]any{attributes}, fields)
}

func (s *Service) projectAuthRelationDocuments(ctx context.Context, db recordidentity.DB, documents []map[string]any, fields []schema.FieldInput) error {
	targetIDs := make(map[string]map[uuid.UUID]string)
	collect := func(name string, id uuid.UUID) (string, error) {
		if targetIDs[name] == nil {
			targetIDs[name] = make(map[uuid.UUID]string)
		}
		canonical := id.String()
		targetIDs[name][id] = canonical
		return canonical, nil
	}
	for _, attributes := range documents {
		if err := rewriteAuthRelationIdentities(attributes, fields, collect); err != nil {
			return err
		}
	}
	for name, ids := range targetIDs {
		target, err := loadRelationIdentityTarget(ctx, db, name)
		if err != nil {
			return err
		}
		if target.kind == "auth" || target.policy.Kind == schema.RecordIDText {
			if err := recordidentity.LoadPublicIDs(ctx, db, target.kind, target.id, ids); err != nil {
				return err
			}
		}
	}
	project := func(name string, id uuid.UUID) (string, error) {
		return targetIDs[name][id], nil
	}
	for _, attributes := range documents {
		if err := rewriteAuthRelationIdentities(attributes, fields, project); err != nil {
			return err
		}
	}
	return nil
}

func rewriteAuthRelationIdentities(attributes map[string]any, fields []schema.FieldInput, lookup func(string, uuid.UUID) (string, error)) error {
	rewrite := func(target string, raw any) (string, error) {
		text, ok := raw.(string)
		if !ok {
			return "", ErrValidation
		}
		id, err := uuid.Parse(text)
		if err != nil {
			return "", ErrValidation
		}
		if id == uuid.Nil {
			return "", recordidentity.ErrInvalid
		}
		return lookup(target, id)
	}
	for _, field := range fields {
		raw := attributes[field.Name]
		if field.Type != schema.TypeRelation || raw == nil {
			continue
		}
		target := field.Relation.TargetCollection
		if field.Relation.Cardinality == schema.RelationMultiple {
			values, ok := raw.([]any)
			if !ok {
				return ErrValidation
			}
			for i, value := range values {
				public, err := rewrite(target, value)
				if err != nil {
					return err
				}
				values[i] = public
			}
			continue
		}
		public, err := rewrite(target, raw)
		if err != nil {
			return err
		}
		attributes[field.Name] = public
	}
	return nil
}
