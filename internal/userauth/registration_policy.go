package userauth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// completePublicRegistration validates a newly inserted user before any session
// or OAuth identity is committed. Caller fields and generated values must obey
// the collection schema and its current create rule in the same transaction.
func (s *Service) completePublicRegistration(ctx context.Context, tx pgx.Tx, user User, input map[string]json.RawMessage) error {
	var enabled bool
	var rule *string
	var raw []byte
	err := tx.QueryRow(ctx, `select registration_enabled,create_rule,fields_json from pgbase_meta.auth_collections where id=$1 for share`, user.AuthCollectionID).Scan(&enabled, &rule, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !enabled || rule == nil || user.AuthCollectionID == DefaultCollectionID && !s.defaultRegistration {
		return ErrInvalidCredentials
	}
	var fields []schema.FieldInput
	if json.Unmarshal(raw, &fields) != nil {
		return ErrValidation
	}
	profile, request, err := s.prepareRegistrationProfile(ctx, tx, user, fields, input)
	if err != nil {
		return err
	}
	profileRaw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into pgbase_meta.user_auth_profiles(user_id,attributes,updated_at) values($1,$2,$3)`, user.ID, profileRaw, user.CreatedAt); err != nil {
		return err
	}
	allowed, err := EvaluateAuthRecordRuleInTx(ctx, tx, user.AuthCollectionID, *rule, AuthRuleContext{
		Principal: identity.Subject{Kind: identity.Anonymous},
		Current:   AuthRecord{ID: user.ID},
		Request:   request,
	})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrInvalidCredentials
	}
	return nil
}
