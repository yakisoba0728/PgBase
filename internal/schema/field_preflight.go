package schema

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/fieldvalue"
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
		if !exists || reflect.DeepEqual(prior.Options, field.Options) {
			continue
		}
		if field.Type != TypeText && field.Type != TypeInteger && field.Type != TypeBigInt && field.Type != TypeNumeric && !(field.Type == TypeJSONB && len(field.Options.AllowedValues) > 0) {
			continue
		}
		rows, err := tx.Query(ctx, "select "+pgx.Identifier{prior.ColumnName}.Sanitize()+"::text from "+pgx.Identifier{"app", before.TableName}.Sanitize()+" where "+pgx.Identifier{prior.ColumnName}.Sanitize()+" is not null")
		if err != nil {
			return err
		}
		valid := true
		for rows.Next() {
			var value string
			if rows.Scan(&value) != nil {
				valid = false
				break
			}
			if field.Type == TypeText {
				valid = fieldvalue.ValidateText(value, fieldValueOptions(field.Options)) == ""
			} else if field.Type == TypeJSONB {
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
