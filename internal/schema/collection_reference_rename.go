package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) renameCollectionReferences(ctx context.Context, collectionID uuid.UUID, newName string) error {
	var oldName string
	if err := s.db.QueryRow(ctx, `select name from pgbase_meta.collections where id=$1 for update`, collectionID).Scan(&oldName); err != nil {
		return fmt.Errorf("read collection name before rename: %w", err)
	}
	if oldName == newName {
		return nil
	}
	rows, err := s.db.Query(ctx, `select collection_id,rules from pgbase_meta.collection_rules for update`)
	if err != nil {
		return fmt.Errorf("read collection rule references: %w", err)
	}
	type changedRule struct {
		collectionID uuid.UUID
		rules        []byte
	}
	changed := []changedRule{}
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var document any
		if json.Unmarshal(raw, &document) != nil {
			rows.Close()
			return ErrCatalogInvalid
		}
		if renameRuleCollectionReferences(document, oldName, newName) {
			encoded, encodeErr := json.Marshal(document)
			if encodeErr != nil {
				rows.Close()
				return encodeErr
			}
			changed = append(changed, changedRule{collectionID: id, rules: encoded})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, rule := range changed {
		if _, err = s.db.Exec(ctx, `update pgbase_meta.collection_rules set rules=$2::jsonb,revision=revision+1,updated_at=clock_timestamp() where collection_id=$1`, rule.collectionID, rule.rules); err != nil {
			return fmt.Errorf("rename collection rule references: %w", err)
		}
	}
	oldPrefix := "@collection." + oldName + "."
	newPrefix := "@collection." + newName + "."
	if _, err = s.db.Exec(ctx, `update pgbase_meta.auth_collections set
		list_rule=replace(list_rule,$1,$2),view_rule=replace(view_rule,$1,$2),create_rule=replace(create_rule,$1,$2),
		update_rule=replace(update_rule,$1,$2),delete_rule=replace(delete_rule,$1,$2),auth_rule=replace(auth_rule,$1,$2),manage_rule=replace(manage_rule,$1,$2),updated_at=clock_timestamp()
		where coalesce(list_rule,'') like '%'||$1||'%' or coalesce(view_rule,'') like '%'||$1||'%' or coalesce(create_rule,'') like '%'||$1||'%' or coalesce(update_rule,'') like '%'||$1||'%' or coalesce(delete_rule,'') like '%'||$1||'%' or coalesce(auth_rule,'') like '%'||$1||'%' or coalesce(manage_rule,'') like '%'||$1||'%'`, oldPrefix, newPrefix); err != nil {
		return fmt.Errorf("rename auth collection rule references: %w", err)
	}
	return nil
}

func renameRuleCollectionReferences(value any, oldName, newName string) bool {
	oldPrefix := "@collection." + oldName + "."
	newPrefix := "@collection." + newName + "."
	changed := false
	var visit func(any, string)
	visit = func(node any, key string) {
		switch typed := node.(type) {
		case map[string]any:
			for childKey, child := range typed {
				if childKey == "value" {
					continue
				}
				if text, ok := child.(string); ok && (childKey == "field" || childKey == "otherField" || childKey == "pocketbase") {
					replaced := strings.ReplaceAll(text, oldPrefix, newPrefix)
					if replaced != text {
						typed[childKey] = replaced
						changed = true
					}
					continue
				}
				visit(child, childKey)
			}
		case []any:
			for _, child := range typed {
				visit(child, key)
			}
		}
	}
	visit(value, "")
	return changed
}
