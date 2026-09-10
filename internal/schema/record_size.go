package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaximumRecordBytes bounds the compact canonical JSON record produced by a
// record write or schema DDL. HTTP wire limits separately account for JSON's
// optional HTML escaping and request envelopes.
const MaximumRecordBytes = 8 << 20

// Validate only changes that can grow a row. Dropping a field must remain a
// repair path for legacy oversized records; changing an existing DEFAULT does
// not rewrite its values. The caller holds the schema write fence and runs this
// after DDL, so a failure rolls back both physical and catalog changes.
func validateChangedRecordSizes(ctx context.Context, tx pgx.Tx, before, after Collection) error {
	existing := make(map[uuid.UUID]FieldType, len(before.Fields))
	for _, field := range before.Fields {
		existing[field.ID] = field.Type
	}
	grows := false
	columns := []string{"root.id", "root.created_at", "root.updated_at"}
	timestamps := []string{"created_at", "updated_at"}
	for _, field := range after.Fields {
		if field.Type == TypeFile {
			continue
		}
		grows = grows || existing[field.ID] != field.Type
		column := "root." + pgx.Identifier{field.ColumnName}.Sanitize()
		switch field.Type {
		case TypeBigInt, TypeNumeric:
			column += "::text"
		case TypeTimestampTZ, TypeAutoDate:
			timestamps = append(timestamps, field.Name)
		case TypeRelation:
			if field.Relation.Cardinality == RelationMultiple {
				column = "(select coalesce(jsonb_agg(target_id order by position), '[]'::jsonb) from " +
					pgx.Identifier{"pgbase_relation", field.Relation.Storage.Name}.Sanitize() + " where source_id = root.id)"
			}
		}
		columns = append(columns, column+" as "+pgx.Identifier{field.Name}.Sanitize())
	}
	if !grows {
		return nil
	}
	rows, err := tx.Query(ctx, "select to_jsonb(record) from (select "+strings.Join(columns, ", ")+" from "+
		pgx.Identifier{"app", after.TableName}.Sanitize()+" as root) as record")
	if err != nil {
		return fmt.Errorf("inspect schema record sizes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var record map[string]json.RawMessage
		if err := rows.Scan(&record); err != nil {
			return fmt.Errorf("decode schema record: %w", err)
		}
		// Match query.EncodeRecord's UTC timestamp spelling. RawMessage keeps
		// JSONB numbers remain lossless and ValidateRecordSize applies the same
		// compact, non-HTML-escaped representation as the record write bound.
		for _, name := range timestamps {
			if string(record[name]) == "null" {
				continue
			}
			var timestamp time.Time
			if err := json.Unmarshal(record[name], &timestamp); err != nil {
				return fmt.Errorf("decode schema record timestamp: %w", err)
			}
			record[name], _ = json.Marshal(timestamp.UTC().Format(time.RFC3339Nano))
		}
		if err := ValidateRecordSize(record); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ValidateRecordSize checks the complete post-image, including relations,
// before a record write or schema change commits. Per-field input bounds
// cannot account for a PATCH merged with existing values. Callers can shrink
// legacy oversized records because the pre-image is never checked here.
func ValidateRecordSize(record any) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("measure record size: %w", err)
	}
	// Encoder.Encode appends exactly one newline that is not part of the JSON.
	if encoded.Len()-1 > MaximumRecordBytes {
		return validation("record", "record_too_large")
	}
	return nil
}
