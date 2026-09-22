package userauth

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/jsonvalue"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func (s *Service) prepareRegistrationProfile(ctx context.Context, tx pgx.Tx, user User, fields []schema.FieldInput, input map[string]json.RawMessage) (map[string]any, map[string]any, error) {
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 64<<10 || !jsonvalue.Valid(raw) {
		return nil, nil, ErrValidation
	}
	var submitted map[string]any
	if decodeAuthProfile(raw, &submitted) != nil {
		return nil, nil, ErrValidation
	}
	definitions := make(map[string]schema.FieldInput, len(fields))
	for _, field := range fields {
		definitions[field.Name] = field
	}
	for name := range submitted {
		field, exists := definitions[name]
		if !exists || reservedRegistrationField(name) || field.Type == schema.TypeAutoDate || field.System != nil && *field.System {
			return nil, nil, ErrValidation
		}
	}
	request := map[string]any{"email": user.Email}
	if input != nil {
		var requested map[string]any
		if decodeAuthProfile(raw, &requested) != nil {
			return nil, nil, ErrValidation
		}
		for name, field := range definitions {
			if _, exists := requested[name]; exists && field.Type == schema.TypePassword {
				requested[name] = "[redacted]"
			}
		}
		request["fields"] = requested
	}
	profile := applyAuthAutoDates(submitted, fields, true, user.CreatedAt)
	if err := s.resolveAuthRelationIdentities(ctx, tx, profile, fields); err != nil {
		return nil, nil, err
	}
	if !validateTypedProfile(profile, fields, false) {
		return nil, nil, ErrValidation
	}
	profile, ok := prepareAuthPasswordFields(fields, profile, true)
	if !ok || !validateTypedProfile(profile, fields, true) {
		return nil, nil, ErrValidation
	}
	if err := validateAuthRelationTargets(ctx, tx, user.ID, profile, fields); err != nil {
		return nil, nil, err
	}
	policy, err := authPolicyTx(ctx, tx, user.AuthCollectionID)
	if err == nil {
		err = validateAuthIdentityUnique(ctx, tx, user.AuthCollectionID, user.ID, profile, policy.IdentityFields)
	}
	return profile, request, err
}

func reservedRegistrationField(name string) bool {
	switch name {
	case "id", "email", "password", "verified", "active", "emailVisibility", "email_visibility", "tokenKey", "token_key", "created", "updated", "createdAt", "updatedAt", "created_at", "updated_at", "authCollectionId", "auth_collection_id", "password_hash", "password_enabled":
		return true
	}
	return false
}

// Profile numbers must survive a read/patch/write cycle without float64 rounding.
func decodeAuthProfile(raw []byte, destination *map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(destination)
}
