package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
	var viewRule, updateRule string
	err := tx.QueryRow(ctx, `select c.fields_json,c.view_rule,c.update_rule from pgbase_meta.auth_collections c join pgbase_meta.users u on u.auth_collection_id=c.id where c.id=$1 and u.id=$2 and u.active`, collectionID, recordID).Scan(&fieldsRaw, &viewRule, &updateRule)
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
		if p.MaximumBytes > s.options.MaximumBytes {
			p.MaximumBytes = s.options.MaximumBytes
		}
		if principal.Kind == identity.Administrator {
			return p, true, nil
		}
		if field.Hidden {
			return FieldPolicy{}, true, ErrNotFound
		}
		if principal.Kind == identity.Anonymous {
			if operation != OperationRead || p.Protected == nil || *p.Protected {
				return FieldPolicy{}, true, ErrDenied
			}
			return p, true, nil
		}
		if principal.Kind != identity.User || principal.ID != recordID {
			return FieldPolicy{}, true, ErrDenied
		}
		rule := viewRule
		if operation == OperationWrite {
			rule = updateRule
		}
		if !authFileRuleAllows(rule, principal.ID, recordID) {
			return FieldPolicy{}, true, ErrDenied
		}
		return p, true, nil
	}
	return FieldPolicy{}, true, ErrInvalid
}

func boolPointer(v bool) *bool { return &v }

func authFileRuleAllows(rule string, principal, target uuid.UUID) bool {
	switch strings.TrimSpace(rule) {
	case "", "true":
		return true
	case "@request.auth.id = id":
		return principal != uuid.Nil && principal == target
	case "@request.auth.id != ''":
		return principal != uuid.Nil
	default:
		return false
	}
}
