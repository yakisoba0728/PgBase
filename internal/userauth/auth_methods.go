package userauth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PublicAuthMethods reports the public login methods configured for an auth
// collection. It deliberately omits provider configuration and all secret state.
func (s *Service) PublicAuthMethods(ctx context.Context, collectionID uuid.UUID) (PublicAuthMethods, error) {
	if err := s.ready(); err != nil {
		return PublicAuthMethods{}, err
	}
	if collectionID == uuid.Nil {
		return PublicAuthMethods{}, ErrValidation
	}
	policy, err := s.authPolicy(ctx, collectionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicAuthMethods{}, ErrNotFound
	}
	if err != nil {
		return PublicAuthMethods{}, err
	}
	rows, err := s.pool.Query(ctx, `select name from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and enabled order by name limit 32`, collectionID)
	if err != nil {
		return PublicAuthMethods{}, err
	}
	defer rows.Close()
	out := PublicAuthMethods{PasswordEnabled: policy.PasswordEnabled, IdentityFields: append([]string(nil), policy.IdentityFields...), OTPEnabled: policy.OTPEnabled, MFAEnabled: policy.MFAEnabled, OAuthProviders: []string{}}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return PublicAuthMethods{}, err
		}
		out.OAuthProviders = append(out.OAuthProviders, provider)
	}
	if err := rows.Err(); err != nil {
		return PublicAuthMethods{}, err
	}
	return out, nil
}
