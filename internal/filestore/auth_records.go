package filestore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) authFieldPolicy(ctx context.Context, tx pgx.Tx, principal identity.Subject, collectionID, recordID uuid.UUID, fieldName string, operation Operation) (FieldPolicy, bool, error) {
	if tx == nil {
		return FieldPolicy{}, false, nil
	}
	var fieldsRaw []byte
	err := tx.QueryRow(ctx, `select c.fields_json from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id where c.id=$1 and u.id=$2 and u.active`, collectionID, recordID).Scan(&fieldsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if e := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.auth_collections where id=$1)`, collectionID).Scan(&exists); e != nil {
			return FieldPolicy{}, false, e
		}
		if !exists {
			return FieldPolicy{}, false, nil
		}
		return FieldPolicy{}, true, ErrNotFound
	}
	if err != nil {
		return FieldPolicy{}, true, err
	}
	if principal.Kind != identity.Anonymous && principal.Kind != identity.Administrator && (principal.Kind != identity.User || principal.ID != recordID) {
		return FieldPolicy{}, true, ErrDenied
	}
	var fields []schema.FieldInput
	if json.Unmarshal(fieldsRaw, &fields) != nil {
		return FieldPolicy{}, true, ErrInvalid
	}
	for _, field := range fields {
		if field.Name != fieldName || field.Type != schema.TypeFile {
			continue
		}
		p := FieldPolicy{MaximumBytes: s.options.MaximumBytes, MaximumCount: 1, Protected: boolPointer(true), Required: field.Required, Hidden: field.Hidden}
		if field.Options != nil {
			if field.Options.MaxFileSize != nil {
				p.MaximumBytes = *field.Options.MaxFileSize
			}
			if field.Options.MaxSelect != nil {
				p.MaximumCount = *field.Options.MaxSelect
			}
			p.MimeTypes = append([]string(nil), field.Options.MimeTypes...)
			p.Thumbs = append([]string(nil), field.Options.Thumbs...)
			if field.Options.Protected != nil {
				p.Protected = field.Options.Protected
			}
		}
		if len(p.MimeTypes) == 0 {
			p.MimeTypes = append([]string(nil), s.options.AllowedTypes...)
		}
		return p, true, nil
	}
	return FieldPolicy{}, true, ErrInvalid
}

func boolPointer(v bool) *bool { return &v }
