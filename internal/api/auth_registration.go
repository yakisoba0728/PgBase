package api

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type userRegistrationFields interface {
	RegisterWithFields(context.Context, string, string, string, map[string]json.RawMessage) (userauth.Session, error)
}

type collectionRegistrationFields interface {
	RegisterInCollectionWithFields(context.Context, uuid.UUID, string, string, string, map[string]json.RawMessage) (userauth.Session, error)
}

type oauthRegistrationFields interface {
	CompleteOAuthWithFields(context.Context, string, string, string, map[string]json.RawMessage) (userauth.Session, error)
}

// Presence is significant: omitted fields retain the original signup contract;
// explicit null and non-object values are malformed profile input.
func decodeRegistrationFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if raw == nil {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if value := bytes.TrimSpace(raw); len(value) == 0 || value[0] != '{' || json.Unmarshal(value, &fields) != nil {
		return nil, userauth.ErrValidation
	}
	return fields, nil
}

func registerWithFields(ctx context.Context, auth UserAuth, email, password, address string, raw json.RawMessage) (userauth.Session, error) {
	fields, err := decodeRegistrationFields(raw)
	if err != nil {
		return userauth.Session{}, err
	}
	if extended, ok := auth.(userRegistrationFields); ok {
		return extended.RegisterWithFields(ctx, email, password, address, fields)
	}
	if len(fields) != 0 {
		return userauth.Session{}, userauth.ErrValidation
	}
	return auth.Register(ctx, email, password, address)
}

func registerCollectionWithFields(ctx context.Context, auth AuthCollections, collectionID uuid.UUID, email, password, address string, raw json.RawMessage) (userauth.Session, error) {
	fields, err := decodeRegistrationFields(raw)
	if err != nil {
		return userauth.Session{}, err
	}
	if extended, ok := auth.(collectionRegistrationFields); ok {
		return extended.RegisterInCollectionWithFields(ctx, collectionID, email, password, address, fields)
	}
	if len(fields) != 0 {
		return userauth.Session{}, userauth.ErrValidation
	}
	return auth.RegisterInCollection(ctx, collectionID, email, password, address)
}

func completeOAuthWithFields(ctx context.Context, auth AuthFactors, provider, state, code string, raw json.RawMessage) (userauth.Session, error) {
	fields, err := decodeRegistrationFields(raw)
	if err != nil {
		return userauth.Session{}, err
	}
	if extended, ok := auth.(oauthRegistrationFields); ok {
		return extended.CompleteOAuthWithFields(ctx, provider, state, code, fields)
	}
	if len(fields) != 0 {
		return userauth.Session{}, userauth.ErrValidation
	}
	return auth.CompleteOAuth(ctx, provider, state, code)
}
