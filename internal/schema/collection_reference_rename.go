package schema

import (
	"bytes"
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
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&document) != nil {
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
	authRows, err := s.db.Query(ctx, `select id,list_rule,view_rule,create_rule,update_rule,delete_rule,auth_rule,manage_rule from pgbase_meta.auth_collections for update`)
	if err != nil {
		return fmt.Errorf("read auth collection rule references: %w", err)
	}
	type changedAuthRules struct {
		id     uuid.UUID
		values [7]*string
	}
	authChanges := []changedAuthRules{}
	for authRows.Next() {
		var value changedAuthRules
		if err = authRows.Scan(&value.id, &value.values[0], &value.values[1], &value.values[2], &value.values[3], &value.values[4], &value.values[5], &value.values[6]); err != nil {
			authRows.Close()
			return err
		}
		rowChanged := false
		for i, source := range value.values {
			if source == nil {
				continue
			}
			replaced := replaceRuleSource(*source, oldPrefix, newPrefix)
			if replaced != *source {
				value.values[i] = &replaced
				rowChanged = true
			}
		}
		if rowChanged {
			authChanges = append(authChanges, value)
		}
	}
	if err = authRows.Err(); err != nil {
		authRows.Close()
		return err
	}
	authRows.Close()
	for _, value := range authChanges {
		if _, err = s.db.Exec(ctx, `update pgbase_meta.auth_collections set list_rule=$2,view_rule=$3,create_rule=$4,update_rule=$5,delete_rule=$6,auth_rule=$7,manage_rule=$8,updated_at=clock_timestamp() where id=$1`, value.id, value.values[0], value.values[1], value.values[2], value.values[3], value.values[4], value.values[5], value.values[6]); err != nil {
			return fmt.Errorf("rename auth collection rule references: %w", err)
		}
	}
	return s.refreshSQLViewDefinitionsAfterRename(ctx, collectionID, newName)
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
					var replaced string
					if childKey == "pocketbase" {
						replaced = replaceRuleSource(text, oldPrefix, newPrefix)
					} else {
						replaced = strings.ReplaceAll(text, oldPrefix, newPrefix)
					}
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

func replaceRuleSource(source, oldPrefix, newPrefix string) string {
	var result strings.Builder
	result.Grow(len(source))
	quote := byte(0)
	escaped := false
	for index := 0; index < len(source); {
		character := source[index]
		if quote != 0 {
			result.WriteByte(character)
			index++
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			result.WriteByte(character)
			index++
			continue
		}
		if strings.HasPrefix(source[index:], oldPrefix) {
			result.WriteString(newPrefix)
			index += len(oldPrefix)
			continue
		}
		result.WriteByte(character)
		index++
	}
	return result.String()
}
