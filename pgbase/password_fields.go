package pgbase

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/passwordfield"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// VerifyRecordPassword verifies a general password field without returning its
// hash. It is a trusted Go extension API; callers supply their own application
// authorization before invoking it. It does not create an HTTP endpoint or an
// authentication session. Missing records and cleared passwords do not match.
func (a *App) VerifyRecordPassword(ctx context.Context, collection string, id uuid.UUID, fieldName, plaintext string) (bool, error) {
	if a == nil || a.pool == nil || id == uuid.Nil || !utf8.ValidString(plaintext) || len(plaintext) > schema.MaximumRecordBytes {
		return false, errors.New("invalid password verification request")
	}
	schemas, ok := a.schemaOperations.(*schema.Service)
	if !ok {
		return false, errors.New("schema service is unavailable")
	}
	var encoded *string
	err := a.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		catalog, err := schemas.EnsureCurrent(ctx, tx)
		if err != nil {
			return err
		}
		definition, exists := catalog.Collections[collection]
		if !exists || definition.View != nil {
			return errors.New("password collection is unavailable")
		}
		var column string
		for _, field := range definition.Fields {
			if field.Name == fieldName && field.Type == schema.TypePassword {
				column = field.ColumnName
				break
			}
		}
		if column == "" {
			return errors.New("password field is unavailable")
		}
		err = tx.QueryRow(ctx, "select "+pgx.Identifier{column}.Sanitize()+" from "+pgx.Identifier{"app", definition.TableName}.Sanitize()+" where id=$1", id).Scan(&encoded)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return false, fmt.Errorf("verify record password: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if encoded == nil || *encoded == "" {
		return false, nil
	}
	match := passwordfield.Verify(*encoded, plaintext) == nil
	if err = ctx.Err(); err != nil {
		return false, err
	}
	return match, nil
}
