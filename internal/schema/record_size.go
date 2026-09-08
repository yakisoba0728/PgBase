package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaximumRecordBytes bounds the JSON record produced by either a record write
// or schema DDL. Half the list page budget leaves room for its envelope/cursor.
const MaximumRecordBytes = 2 << 20

// Validate only changes that can grow a row. Dropping a field must remain a
// repair path for legacy oversized records; changing an existing DEFAULT does
// not rewrite its values. The caller holds the schema write fence and runs this
// after DDL, so a failure rolls back both physical and catalog changes.
func validateChangedRecordSizes(ctx context.Context, tx pgx.Tx, before, after Collection) error {
	existing := make(map[string]FieldType, len(before.Fields))
	for _, field := range before.Fields {
		existing[field.Name] = field.Type
	}
	grows := false
	columns := []string{"root.id", "root.created_at", "root.updated_at"}
	timestamps := []string{"created_at", "updated_at"}
	for _, field := range after.Fields {
		grows = grows || existing[field.Name] != field.Type
		column := "root." + pgx.Identifier{field.ColumnName}.Sanitize()
		switch field.Type {
		case TypeBigInt, TypeNumeric:
			column += "::text"
		case TypeTimestampTZ:
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
		// JSONB numbers lossless; Marshal applies the same compact/HTML escaping
		// as the record write bound, rather than measuring PostgreSQL text bytes.
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
		encoded, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("measure schema record size: %w", err)
		}
		if len(encoded) > MaximumRecordBytes {
			return validation("record", "record_too_large")
		}
	}
	return rows.Err()
}
