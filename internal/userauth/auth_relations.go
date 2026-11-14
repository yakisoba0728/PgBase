package userauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func validateAuthRelationTargets(ctx context.Context, tx pgx.Tx, owner uuid.UUID, attributes map[string]any, fields []schema.FieldInput) error {
	for _, field := range fields {
		if field.Type == schema.TypeFile {
			values, _ := attributes[field.Name].([]any)
			for _, raw := range values {
				id := uuid.MustParse(raw.(string))
				var exists bool
				if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_files where id=$1 and auth_user_id=$2 and status='ready' and field_name=$3)`, id, owner, field.Name).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return ErrValidation
				}
			}
			continue
		}
		if field.Type != schema.TypeRelation {
			continue
		}
		ids := relationIDs(attributes[field.Name], field)
		for _, id := range ids {
			var authTarget uuid.UUID
			err := tx.QueryRow(ctx, `select u.id from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id where c.name=$1 and u.id=$2 and u.active for key share of u`, field.Relation.TargetCollection, id).Scan(&authTarget)
			if err == nil {
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			var tableName string
			err = tx.QueryRow(ctx, `select c.table_name from pgbase_meta.collections c join pgbase_meta.record_revisions r on r.collection_id=c.id where c.name=$1 and r.record_id=$2 and c.collection_type='base'`, field.Relation.TargetCollection, id).Scan(&tableName)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrValidation
			}
			if err != nil {
				return err
			}
			var nativeTarget uuid.UUID
			err = tx.QueryRow(ctx, `select id from `+pgx.Identifier{"app", tableName}.Sanitize()+` where id=$1 for key share`, id).Scan(&nativeTarget)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrValidation
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAuthRelationDefinitions(ctx context.Context, tx pgx.Tx, sourceName string, fields []schema.FieldInput) error {
	for _, field := range fields {
		if field.Type != schema.TypeRelation {
			continue
		}
		if field.Relation.TargetCollection == sourceName {
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

// RelationDeleteTarget identifies a caller-owned root of a record deletion.
type RelationDeleteTarget struct {
	Collection string
	ID         uuid.UUID
}

func enforceAuthRelationDeletes(ctx context.Context, tx pgx.Tx, targetCollection string, target uuid.UUID, authorize func(pgx.Tx, uuid.UUID, uuid.UUID) error) error {
	return EnforceRelationDeletes(ctx, tx, []RelationDeleteTarget{{Collection: targetCollection, ID: target}}, authorize)
}

// EnforceRelationDeletes applies incoming auth-profile policies to the complete
// caller-owned deletion set before the caller removes its root records.
func EnforceRelationDeletes(ctx context.Context, tx pgx.Tx, targets []RelationDeleteTarget, authorize func(pgx.Tx, uuid.UUID, uuid.UUID) error) error {
	if len(targets) == 0 {
		return nil
	}
	type recordKey struct {
		collection string
		id         uuid.UUID
	}
	closure := make([]recordKey, 0, len(targets))
	roots := map[recordKey]bool{}
	deleted := map[recordKey]bool{}
	collectionNames := []string{}
	seenCollections := map[string]bool{}
	for _, target := range targets {
		if target.Collection == "" || target.ID == uuid.Nil {
			return ErrValidation
		}
		if !seenCollections[target.Collection] {
			seenCollections[target.Collection] = true
			collectionNames = append(collectionNames, target.Collection)
		}
		key := recordKey{target.Collection, target.ID}
		if !roots[key] {
			roots[key], deleted[key] = true, true
			closure = append(closure, key)
		}
	}
	// Pin definitions until commit; unrelated deletes never need profile row locks.
	metadata, err := tx.Query(ctx, `select exists(select 1 from jsonb_array_elements(c.fields_json) field where field->>'type'='relation' and field->'relation'->>'targetCollection'=any($1::text[])) from pgbase_meta.auth_collections c order by c.id for share of c`, collectionNames)
	if err != nil {
		return err
	}
	related := false
	for metadata.Next() {
		var referencesRoot bool
		if err := metadata.Scan(&referencesRoot); err != nil {
			metadata.Close()
			return err
		}
		related = related || referencesRoot
	}
	if err := metadata.Err(); err != nil {
		metadata.Close()
		return err
	}
	metadata.Close()
	if !related {
		return nil
	}
	rows, err := tx.Query(ctx, `select c.id,c.name,c.fields_json,p.user_id,p.attributes from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id join pgbase_meta.user_auth_profiles p on p.user_id=u.id order by p.user_id for update of p`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type profile struct {
		key          recordKey
		collectionID uuid.UUID
		attrs        map[string]any
		changed      bool
	}
	type reference struct {
		source *profile
		field  schema.FieldInput
	}
	profiles := []*profile{}
	incoming := map[recordKey][]reference{}
	for rows.Next() {
		value := &profile{}
		var fieldsRaw, attrsRaw []byte
		if err := rows.Scan(&value.collectionID, &value.key.collection, &fieldsRaw, &value.key.id, &attrsRaw); err != nil {
			return err
		}
		var fields []schema.FieldInput
		if json.Unmarshal(fieldsRaw, &fields) != nil || decodeAuthProfile(attrsRaw, &value.attrs) != nil {
			return errors.New("invalid stored auth relation")
		}
		profiles = append(profiles, value)
		for _, field := range fields {
			if field.Type != schema.TypeRelation {
				continue
			}
			for _, id := range relationIDs(value.attrs[field.Name], field) {
				key := recordKey{field.Relation.TargetCollection, id}
				incoming[key] = append(incoming[key], reference{value, field})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for index := 0; index < len(closure); index++ {
		for _, ref := range incoming[closure[index]] {
			if ref.field.Relation.OnDelete == schema.RelationCascade && !deleted[ref.source.key] {
				deleted[ref.source.key] = true
				closure = append(closure, ref.source.key)
			}
		}
	}
	// Check the complete closure before writing, including references to cascaded records.
	for _, key := range closure {
		for _, ref := range incoming[key] {
			if deleted[ref.source.key] {
				continue
			}
			switch ref.field.Relation.OnDelete {
			case schema.RelationRestrict:
				return ErrConflict
			case schema.RelationSetNull:
				if ref.field.Relation.Cardinality == schema.RelationMultiple {
					next := []any{}
					for _, raw := range ref.source.attrs[ref.field.Name].([]any) {
						if raw.(string) != key.id.String() {
							next = append(next, raw)
						}
					}
					ref.source.attrs[ref.field.Name] = next
				} else {
					delete(ref.source.attrs, ref.field.Name)
				}
				ref.source.changed = true
			}
		}
	}
	// Authorize every cascade before effects alter profiles used by the rules.
	if authorize != nil {
		for _, value := range profiles {
			if !roots[value.key] && deleted[value.key] {
				if err := authorize(tx, value.collectionID, value.key.id); err != nil {
					return err
				}
			}
		}
	}
	for _, value := range profiles {
		if !roots[value.key] && deleted[value.key] {
			if _, err := tx.Exec(ctx, `delete from pgbase_meta.users where id=$1`, value.key.id); err != nil {
				return err
			}
		} else if value.changed {
			raw, err := json.Marshal(value.attrs)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `update pgbase_meta.user_auth_profiles set attributes=$2,updated_at=current_timestamp where user_id=$1`, value.key.id, raw); err != nil {
				return err
			}
		}
	}
	return nil
}
