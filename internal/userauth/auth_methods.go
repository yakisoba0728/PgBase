package userauth

import (
	"context"
	"errors"
	"net/url"
	"sort"

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
	var collectionName string
	if err := s.pool.QueryRow(ctx, `select name from pgbase_meta.auth_collections where id=$1`, collectionID).Scan(&collectionName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PublicAuthMethods{}, ErrNotFound
		}
		return PublicAuthMethods{}, err
	}
	rows, err := s.pool.Query(ctx, `select name from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and enabled order by name limit 32`, collectionID)
	if err != nil {
		return PublicAuthMethods{}, err
	}
	defer rows.Close()
	startEndpoint := "/api/v1/auth/" + url.PathEscape(collectionName) + "/oauth/start"
	out := PublicAuthMethods{PasswordEnabled: policy.PasswordEnabled, IdentityFields: append([]string(nil), policy.IdentityFields...), OTPEnabled: policy.OTPEnabled, MFAEnabled: policy.MFAEnabled, OAuthProviders: []string{}, OAuth2: OAuth2Methods{Providers: []OAuthMethodProvider{}}}
	providers := make(map[string]struct{}, len(s.oauthProviders))
	for name := range s.oauthProviders {
		providers[name] = struct{}{}
	}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return PublicAuthMethods{}, err
		}
		providers[provider] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return PublicAuthMethods{}, err
	}
	names := make([]string, 0, len(providers))
	for provider := range providers {
		names = append(names, provider)
	}
	sort.Strings(names)
	for _, provider := range names {
		out.OAuthProviders = append(out.OAuthProviders, provider)
		out.OAuth2.Providers = append(out.OAuth2.Providers, OAuthMethodProvider{Name: provider, StartEndpoint: startEndpoint})
	}
	out.OAuth2.Enabled = len(names) > 0
	return out, nil
}
