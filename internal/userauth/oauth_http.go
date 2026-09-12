package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maximumOAuthResponseBytes = 64 << 10

// HTTPOAuthConfig describes a standard authorization-code OAuth2 provider.
// SubjectField, EmailField and EmailVerifiedField are top-level JSON fields.
type HTTPOAuthConfig struct {
	// ProviderName selects a built-in profile normalizer. Empty preserves the
	// configurable top-level-field behavior for custom OAuth/OIDC servers.
	ProviderName          string
	AuthorizationURL      string
	TokenURL              string
	UserInfoURL           string
	Issuer                string
	JWKSURL               string
	ClientID              string
	ClientSecret          string
	Scopes                []string
	SubjectField          string
	EmailField            string
	EmailVerifiedField    string
	AvatarField           string
	HTTPClient            *http.Client
	AllowInsecureLoopback bool
}

type HTTPOAuthProvider struct {
	config HTTPOAuthConfig
	client *http.Client
	apple  appleKeyCache
}

func NewHTTPOAuthProvider(config HTTPOAuthConfig) (*HTTPOAuthProvider, error) {
	if !validEndpoint(config.AuthorizationURL, config.AllowInsecureLoopback) || !validEndpoint(config.TokenURL, config.AllowInsecureLoopback) || (config.UserInfoURL != "" && !validEndpoint(config.UserInfoURL, config.AllowInsecureLoopback)) || (config.JWKSURL != "" && (!validEndpoint(config.JWKSURL, config.AllowInsecureLoopback) || !validEndpoint(config.Issuer, config.AllowInsecureLoopback))) || (config.JWKSURL == "" && config.UserInfoURL == "") ||
		config.ClientID == "" || len(config.ClientID) > 1024 || len(config.ClientSecret) > 4096 {
		return nil, ErrValidation
	}
	if config.SubjectField == "" {
		config.SubjectField = "sub"
	}
	if config.EmailField == "" {
		config.EmailField = "email"
	}
	if config.EmailVerifiedField == "" {
		config.EmailVerifiedField = "email_verified"
	}
	if config.AvatarField == "" {
		config.AvatarField = "picture"
	}
	for _, field := range []string{config.SubjectField, config.EmailField, config.EmailVerifiedField, config.AvatarField} {
		if field == "" || len(field) > 128 {
			return nil, ErrValidation
		}
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPOAuthProvider{config: config, client: client}, nil
}

func (p *HTTPOAuthProvider) AuthorizationURL(state, challenge, redirectURI string) (string, error) {
	u, err := url.Parse(p.config.AuthorizationURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.config.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(p.config.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if p.config.ProviderName == "apple" || p.config.JWKSURL != "" {
		q.Set("nonce", challenge)
	}
	if p.config.ProviderName == "apple" {
		q.Set("response_mode", "form_post")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *HTTPOAuthProvider) Exchange(ctx context.Context, code, verifier, redirectURI string) (OAuthIdentity, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "client_id": {p.config.ClientID}, "code_verifier": {verifier}}
	if p.config.ClientSecret != "" {
		form.Set("client_secret", p.config.ClientSecret)
	}
	basicTokenAuth := p.config.ProviderName == "bitbucket" || p.config.ProviderName == "notion"
	if basicTokenAuth {
		form.Del("client_id")
		form.Del("client_secret")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthIdentity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicTokenAuth {
		req.SetBasicAuth(p.config.ClientID, p.config.ClientSecret)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		IDToken     string `json:"id_token"`
	}
	if err := p.doJSON(req, &token); err != nil {
		return OAuthIdentity{}, err
	}
	if token.AccessToken == "" || len(token.AccessToken) > 8192 || token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer") {
		return OAuthIdentity{}, errors.New("OAuth token response is invalid")
	}
	if p.config.ProviderName == "apple" {
		return p.exchangeAppleIdentity(ctx, token.IDToken, verifier)
	}
	if p.config.JWKSURL != "" && p.config.UserInfoURL == "" {
		fields, err := p.verifyOIDCIDToken(ctx, token.IDToken, verifier)
		if err != nil {
			return OAuthIdentity{}, err
		}
		return p.identityFromFields(fields)
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, p.config.UserInfoURL, nil)
	if err != nil {
		return OAuthIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	switch p.config.ProviderName {
	case "twitch":
		req.Header.Set("Client-Id", p.config.ClientID)
	case "trakt":
		req.Header.Set("trakt-api-key", p.config.ClientID)
		req.Header.Set("trakt-api-version", "2")
	case "linear", "monday":
		query := `{"query":"query { viewer { id name email avatarUrl } }"}`
		if p.config.ProviderName == "monday" {
			query = `{"query":"query { me { id name email photo_thumb is_verified } }"}`
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, p.config.UserInfoURL, strings.NewReader(query))
		if err != nil {
			return OAuthIdentity{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		req.Header.Set("Content-Type", "application/json")
	}
	fields := make(map[string]json.RawMessage)
	if err := p.doJSON(req, &fields); err != nil {
		return OAuthIdentity{}, err
	}
	if p.config.ProviderName != "" {
		if identity, handled, err := normalizeProviderProfile(p.config.ProviderName, fields, token.AccessToken, p); handled {
			identity.AvatarAllowInsecureLoopback = p.config.AllowInsecureLoopback
			return identity, err
		}
	}
	return p.identityFromFields(fields)
}

func (p *HTTPOAuthProvider) identityFromFields(fields map[string]json.RawMessage) (OAuthIdentity, error) {
	var identity OAuthIdentity
	if err := json.Unmarshal(fields[p.config.SubjectField], &identity.Subject); err != nil {
		return OAuthIdentity{}, errors.New("OAuth subject is invalid")
	}
	if err := json.Unmarshal(fields[p.config.EmailField], &identity.Email); err != nil {
		return OAuthIdentity{}, errors.New("OAuth email is invalid")
	}
	if raw, ok := fields[p.config.EmailVerifiedField]; ok {
		if err := json.Unmarshal(raw, &identity.EmailVerified); err != nil {
			return OAuthIdentity{}, errors.New("OAuth email verification is invalid")
		}
	}
	if raw, ok := fields[p.config.AvatarField]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &identity.AvatarURL); err != nil {
			return OAuthIdentity{}, errors.New("OAuth avatar is invalid")
		}
	}
	identity.AvatarAllowInsecureLoopback = p.config.AllowInsecureLoopback
	return identity, nil
}

func (p *HTTPOAuthProvider) doJSON(req *http.Request, destination any) error {
	response, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumOAuthResponseBytes))
		return fmt.Errorf("OAuth endpoint status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumOAuthResponseBytes+1))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("OAuth response is too large or malformed")
	}
	return nil
}

func validEndpoint(raw string, allowInsecureLoopback bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return allowInsecureLoopback && u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback()))
}
