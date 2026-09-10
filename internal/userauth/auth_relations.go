package userauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func validateAuthRelationTargets(ctx context.Context, tx pgx.Tx, attributes map[string]any, fields []schema.FieldInput) error {
	for _, field := range fields {
		if field.Type != schema.TypeRelation {
			continue
		}
		ids := relationIDs(attributes[field.Name], field)
		for _, id := range ids {
			var exists bool
			err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id where c.name=$1 and u.id=$2 and u.active) or exists(select 1 from pgbase_meta.collections c join pgbase_meta.record_revisions r on r.collection_id=c.id where c.name=$1 and r.record_id=$2)`, field.Relation.TargetCollection, id).Scan(&exists)
			if err != nil {
				return err
			}
			if !exists {
				return ErrValidation
			}
		}
	}
	return nil
}

func validateAuthRelationDefinitions(ctx context.Context, tx pgx.Tx, fields []schema.FieldInput) error {
	for _, field := range fields {
		if field.Type != schema.TypeRelation {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.auth_collections where name=$1) or exists(select 1 from pgbase_meta.collections where name=$1)`, field.Relation.TargetCollection).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrValidation
		}
	}
	return nil
}

func relationIDs(value any, field schema.FieldInput) []uuid.UUID {
	if value == nil {
		return nil
	}
	if field.Relation.Cardinality == schema.RelationMultiple {
		raw, _ := value.([]any)
		out := make([]uuid.UUID, 0, len(raw))
		for _, v := range raw {
			id, e := uuid.Parse(v.(string))
			if e == nil {
				out = append(out, id)
			}
		}
		return out
	}
	v, _ := value.(string)
	id, e := uuid.Parse(v)
	if e != nil {
		return nil
	}
	return []uuid.UUID{id}
}

// enforceAuthRelationDeletes applies incoming relation policies before an auth
// identity is removed. It is transactionally serialized with profile writes.
func enforceAuthRelationDeletes(ctx context.Context, tx pgx.Tx, targetCollection string, target uuid.UUID) error {
	rows, err := tx.Query(ctx, `select c.id,c.fields_json,p.user_id,p.attributes from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id join pgbase_meta.user_auth_profiles p on p.user_id=u.id for update of p`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type change struct {
		user    uuid.UUID
		attrs   map[string]any
		cascade bool
	}
	changes := []change{}
	for rows.Next() {
		var collection, user uuid.UUID
		var fieldsRaw, attrsRaw []byte
		if err := rows.Scan(&collection, &fieldsRaw, &user, &attrsRaw); err != nil {
			return err
		}
		var fields []schema.FieldInput
		var attrs map[string]any
		if json.Unmarshal(fieldsRaw, &fields) != nil || json.Unmarshal(attrsRaw, &attrs) != nil {
			return errors.New("invalid stored auth relation")
		}
		changed, cascade := false, false
		for _, field := range fields {
			if field.Type != schema.TypeRelation || field.Relation.TargetCollection != targetCollection {
				continue
			}
			ids := relationIDs(attrs[field.Name], field)
			found := false
			for _, id := range ids {
				found = found || id == target
			}
			if !found {
				continue
			}
			switch field.Relation.OnDelete {
			case schema.RelationRestrict:
				return ErrConflict
			case schema.RelationCascade:
				cascade = true
			case schema.RelationSetNull:
				if field.Relation.Cardinality == schema.RelationMultiple {
					next := []any{}
					for _, v := range attrs[field.Name].([]any) {
						if v.(string) != target.String() {
							next = append(next, v)
						}
					}
					attrs[field.Name] = next
				} else {
					delete(attrs, field.Name)
				}
				changed = true
			}
		}
		if changed || cascade {
			changes = append(changes, change{user, attrs, cascade})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, c := range changes {
		if c.cascade {
			if _, err := tx.Exec(ctx, `delete from pgbase_meta.users where id=$1`, c.user); err != nil {
				return err
			}
		} else {
			raw, _ := json.Marshal(c.attrs)
			if _, err := tx.Exec(ctx, `update pgbase_meta.user_auth_profiles set attributes=$2,updated_at=current_timestamp where user_id=$1`, c.user, raw); err != nil {
				return err
			}
		}
	}
	return nil
}
