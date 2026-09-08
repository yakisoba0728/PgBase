package pgbase

import (
	"fmt"
	"time"

	"github.com/webisnoteasy/PgBase/internal/userauth"
)

// OAuthProviderOptions describes a standard OAuth2 authorization-code provider.
// Redirects are exact application callback URLs. Provider credentials are
// configuration secrets and never included in status responses.
type OAuthProviderOptions struct {
	AllowInsecureLoopback bool     `json:"allowInsecureLoopback"`
	Name                  string   `json:"name"`
	AuthorizationURL      string   `json:"authorizationUrl"`
	TokenURL              string   `json:"tokenUrl"`
	UserInfoURL           string   `json:"userInfoUrl"`
	ClientID              string   `json:"clientId"`
	ClientSecret          string   `json:"clientSecret"`
	Scopes                []string `json:"scopes"`
	SubjectField          string   `json:"subjectField"`
	EmailField            string   `json:"emailField"`
	EmailVerifiedField    string   `json:"emailVerifiedField"`
}

func (o OAuthProviderOptions) config() userauth.HTTPOAuthConfig {
	return userauth.HTTPOAuthConfig{AllowInsecureLoopback: o.AllowInsecureLoopback, AuthorizationURL: o.AuthorizationURL, TokenURL: o.TokenURL, UserInfoURL: o.UserInfoURL, ClientID: o.ClientID, ClientSecret: o.ClientSecret, Scopes: append([]string(nil), o.Scopes...), SubjectField: o.SubjectField, EmailField: o.EmailField, EmailVerifiedField: o.EmailVerifiedField}
}

func (o Options) validateOAuth() error {
	if len(o.OAuthProviders) > 16 || len(o.OAuthRedirects) > 32 {
		return fmt.Errorf("OAuth: too many providers or redirects")
	}
	if len(o.OAuthProviders) == 0 && len(o.OAuthRedirects) == 0 {
		return nil
	}
	providers, err := oauthProviders(o.OAuthProviders)
	if err != nil {
		return err
	}
	// Reuse domain validation without opening a database or contacting providers.
	auth := userauth.NewService(nil, nil, o.TokenSecret, 0)
	if err := auth.ConfigureOAuth(providers, o.OAuthRedirects, 5*time.Minute); err != nil {
		return fmt.Errorf("OAuth: invalid provider or redirect configuration")
	}
	return nil
}

func oauthProviders(options []OAuthProviderOptions) (map[string]userauth.OAuthProvider, error) {
	result := make(map[string]userauth.OAuthProvider, len(options))
	for _, option := range options {
		if _, exists := result[option.Name]; exists {
			return nil, fmt.Errorf("OAuth: duplicate provider name")
		}
		provider, err := userauth.NewHTTPOAuthProvider(option.config())
		if err != nil {
			return nil, fmt.Errorf("OAuth: invalid provider configuration")
		}
		result[option.Name] = provider
	}
	return result, nil
}
