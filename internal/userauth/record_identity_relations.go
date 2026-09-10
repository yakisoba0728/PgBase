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
		union all select id,'base'::text,record_id from pgbase_meta.collections where name=$1
	) select id,kind,record_id from targets where (select count(*) from targets)=1`, name).Scan(&target.id, &target.kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return target, ErrValidation
	}
	if err != nil || json.Unmarshal(raw, &target.policy) != nil {
		return target, err
	}
	return target, nil
}

func (s *Service) resolveRelationPublicID(ctx context.Context, db recordidentity.DB, target relationIdentityTarget, public string) (uuid.UUID, error) {
	if target.kind == "auth" {
		return s.recordIdentities.ResolveAuth(ctx, db, target.id, target.policy, public)
	}
	return s.recordIdentities.Resolve(ctx, db, schema.Collection{ID: target.id, RecordID: target.policy}, public)
}

func (s *Service) publicRelationID(ctx context.Context, db recordidentity.DB, target relationIdentityTarget, internal uuid.UUID) (string, error) {
	if target.kind == "auth" {
		return s.recordIdentities.PublicAuth(ctx, db, target.id, internal)
	}
	return s.recordIdentities.Public(ctx, db, schema.Collection{ID: target.id, RecordID: target.policy}, internal)
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
				id, err := uuid.Parse(raw.(string))
				if err != nil {
					return ErrValidation
				}
				values[i], err = s.publicRelationID(ctx, db, target, id)
				if err != nil {
					return err
				}
			}
			continue
		}
		id, err := uuid.Parse(attributes[field.Name].(string))
		if err != nil {
			return ErrValidation
		}
		attributes[field.Name], err = s.publicRelationID(ctx, db, target, id)
		if err != nil {
			return err
		}
	}
	return nil
}
