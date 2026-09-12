package schema

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/fieldvalue"
)

// validateChangedFieldOptions rejects a tightened contract while the old data
// is still visible. Runtime validation alone cannot protect rows written by an
// operator or an older binary.
func validateChangedFieldOptions(ctx context.Context, tx pgx.Tx, before, after Collection) error {
	old := make(map[[16]byte]Field, len(before.Fields))
	for _, field := range before.Fields {
		old[field.ID] = field
	}
	for _, field := range after.Fields {
		prior, exists := old[field.ID]
		modeTightened := before.ValidationMode != ValidationModePocketBase && after.ValidationMode == ValidationModePocketBase && field.Required
		if !exists || !modeTightened && reflect.DeepEqual(prior.Options, field.Options) && (field.Type != TypeFile || prior.Required == field.Required) {
			continue
		}
		if field.Type == TypeFile {
			if err := validateExistingFiles(ctx, tx, before.ID, prior.Name, field); err != nil {
				return err
			}
			continue
		}
		// Password columns contain irreversible Argon2id hashes. Existing values
		// cannot be checked against a new plaintext length/pattern contract; the
		// changed policy applies to the next explicit password write.
		if field.Type == TypePassword {
			continue
		}
		if field.Type != TypeBoolean && field.Type != TypeText && field.Type != TypeEditor && field.Type != TypeInteger && field.Type != TypeBigInt && field.Type != TypeNumeric && field.Type != TypeTimestampTZ && field.Type != TypeJSONB {
			continue
		}
		column := pgx.Identifier{prior.ColumnName}.Sanitize()
		selection := column + "::text"
		if field.Type == TypeTimestampTZ {
			selection = column
		}
		rows, err := tx.Query(ctx, "select "+selection+" from "+pgx.Identifier{"app", before.TableName}.Sanitize()+" where "+column+" is not null")
		if err != nil {
			return err
		}
		valid := true
		for rows.Next() {
			if field.Type == TypeTimestampTZ {
				var parsed time.Time
				if rows.Scan(&parsed) != nil {
					valid = false
					break
				}
				if field.Options.Min != nil {
					bound, e := time.Parse(time.RFC3339Nano, *field.Options.Min)
					valid = e == nil && !parsed.Before(bound)
				}
				if valid && field.Options.Max != nil {
					bound, e := time.Parse(time.RFC3339Nano, *field.Options.Max)
					valid = e == nil && !parsed.After(bound)
				}
				if !valid {
					break
				}
				continue
			}
			var value string
			if rows.Scan(&value) != nil {
				valid = false
				break
			}
			if modeTightened && pocketBaseBlankStored(field.Type, value) {
				valid = false
			} else if field.Type == TypeText {
				valid = fieldvalue.ValidateText(value, fieldValueOptions(field.Options)) == ""
			} else if field.Type == TypeEditor {
				valid = int64(len(value)) <= editorMaximumSize(&field.Options)
			} else if field.Type == TypeJSONB {
				if field.Options.MaxSize != nil && int64(len(value)) > *field.Options.MaxSize {
					valid = false
					continue
				}
				if len(field.Options.AllowedValues) == 0 {
					valid = true
					continue
				}
				var values []string
				valid = json.Unmarshal([]byte(value), &values) == nil && values != nil && fieldvalue.ValidateSelection(values, fieldValueOptions(field.Options)) == ""
			} else {
				valid = fieldvalue.ValidateNumeric(value, fieldValueOptions(field.Options)) == ""
			}
			if !valid {
				break
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if !valid {
			return ErrDataConstraint
		}
	}
	return nil
}

func pocketBaseBlankStored(kind FieldType, value string) bool {
	switch kind {
	case TypeBoolean:
		return value == "false"
	case TypeInteger, TypeBigInt, TypeNumeric:
		return strings.Trim(strings.ReplaceAll(strings.TrimPrefix(value, "-"), ".", ""), "0") == ""
	case TypeText, TypeEditor:
		return value == ""
	case TypeJSONB:
		trimmed := strings.TrimSpace(value)
		return trimmed == "null" || trimmed == `""` || trimmed == "[]" || trimmed == "{}"
	}
	return false
}

func validateExistingFiles(ctx context.Context, tx pgx.Tx, collectionID uuid.UUID, oldName string, field Field) error {
	rows, err := tx.Query(ctx, `select record_id,size_bytes,content_type from pgbase_meta.record_files where collection_id=$1 and field_name=$2 and status='ready' order by record_id`, collectionID, oldName)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts := map[uuid.UUID]int{}
	maximum := fileMaximumSize(field.Options)
	for rows.Next() {
		var recordID uuid.UUID
		var size int64
		var contentType string
		if err := rows.Scan(&recordID, &size, &contentType); err != nil {
			return err
		}
		counts[recordID]++
		if size > maximum || !fileMimeAllowed(contentType, field.Options.MimeTypes) {
			return ErrDataConstraint
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, count := range counts {
		if count > fileMaximumCount(field.Options) {
			return ErrDataConstraint
		}
	}
	if field.Required {
		var missing bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_revisions r where r.collection_id=$1 and not exists(select 1 from pgbase_meta.record_files f where f.collection_id=r.collection_id and f.record_id=r.record_id and f.field_name=$2 and f.status='ready'))`, collectionID, oldName).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return ErrDataConstraint
		}
	}
	return nil
}

func fileMimeAllowed(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == value || strings.HasSuffix(candidate, "/*") && strings.HasPrefix(value, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}
