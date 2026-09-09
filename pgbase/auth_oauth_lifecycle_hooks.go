package pgbase

import (
	"context"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

func (h hookedUserAuth) oauthBegin(ctx context.Context, realm, user uuid.UUID, provider string, operation func() (userauth.OAuthAuthorization, error)) (result userauth.OAuthAuthorization, err error) {
	event, finish, err := h.begin(ctx, HookAuthOAuthBegin, "oauth.begin", realm, user, provider)
	defer func() { finish(err) }()
	if err != nil {
		return result, err
	}
	result, err = operation()
	if err == nil && !result.ExpiresAt.IsZero() {
		event.ChallengeExpiresAt = result.ExpiresAt
	}
	return result, err
}

func (h hookedUserAuth) BeginOAuth(ctx context.Context, provider, redirect, address string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, userauth.DefaultCollectionID, uuid.Nil, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuth(ctx, provider, redirect, address)
	})
}
func (h hookedUserAuth) BeginOAuthWithProviderRedirect(ctx context.Context, provider, returnURI, providerRedirect, address string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, userauth.DefaultCollectionID, uuid.Nil, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuthWithProviderRedirect(ctx, provider, returnURI, providerRedirect, address)
	})
}
func (h hookedUserAuth) BeginOAuthInCollection(ctx context.Context, realm uuid.UUID, provider, redirect, address string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, realm, uuid.Nil, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuthInCollection(ctx, realm, provider, redirect, address)
	})
}
func (h hookedUserAuth) BeginOAuthInCollectionWithProviderRedirect(ctx context.Context, realm uuid.UUID, provider, returnURI, providerRedirect, address string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, realm, uuid.Nil, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuthInCollectionWithProviderRedirect(ctx, realm, provider, returnURI, providerRedirect, address)
	})
}
func (h hookedUserAuth) BeginOAuthLink(ctx context.Context, user uuid.UUID, password, provider, redirect string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, uuid.Nil, user, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuthLink(ctx, user, password, provider, redirect)
	})
}
func (h hookedUserAuth) BeginOAuthLinkWithProviderRedirect(ctx context.Context, user uuid.UUID, password, provider, returnURI, providerRedirect string) (userauth.OAuthAuthorization, error) {
	return h.oauthBegin(ctx, uuid.Nil, user, provider, func() (userauth.OAuthAuthorization, error) {
		return h.Service.BeginOAuthLinkWithProviderRedirect(ctx, user, password, provider, returnURI, providerRedirect)
	})
}
func (h hookedUserAuth) CompleteOAuth(ctx context.Context, provider, state, code string) (userauth.Session, error) {
	return h.session(ctx, HookAuthOAuthComplete, "oauth.complete", uuid.Nil, uuid.Nil, provider, func() (userauth.Session, error) { return h.Service.CompleteOAuth(ctx, provider, state, code) })
}
