//go:build integration

package userauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
)

func TestAppleOAuthDatabaseBoundBridgeFlow(t *testing.T) {
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, expectedNonce, exchangeRedirect string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{rsaJWK("apple-key", &key.PublicKey)}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				http.Error(w, "bad", 400)
				return
			}
			exchangeRedirect = r.Form.Get("redirect_uri")
			verifierDigest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if got := base64.RawURLEncoding.EncodeToString(verifierDigest[:]); got != expectedNonce {
				t.Errorf("token nonce binding=%q want=%q", got, expectedNonce)
			}
			claims := map[string]any{"iss": issuer, "aud": "client", "sub": "apple-e2e", "email": "apple-e2e@example.test", "email_verified": true, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": expectedNonce}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": signTestJWT(t, key, "apple-key", claims)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: "apple", AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/keys", ClientID: "client", ClientSecret: "signed-secret", Scopes: []string{"email"}, HTTPClient: server.Client(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(newUserAuthPool(t), adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	frontend, bridge := "https://app.example/oauth/callback", "https://pgbase.example/api/v1/auth/oauth/apple/callback"
	if err := service.ConfigureOAuth(map[string]OAuthProvider{"apple": provider}, []string{frontend}, time.Minute); err != nil {
		t.Fatal(err)
	}
	service.ConfigureOAuthRegistration(true)
	service.ConfigureDefaultRegistration(true)
	authorization, err := service.BeginOAuthWithProviderRedirect(ctx, "apple", frontend, bridge, "apple-e2e-network")
	if err != nil {
		t.Fatal(err)
	}
	authURL, _ := url.Parse(authorization.AuthorizationURL)
	state := authURL.Query().Get("state")
	expectedNonce = authURL.Query().Get("nonce")
	if authURL.Query().Get("redirect_uri") != bridge || authURL.Query().Get("response_mode") != "form_post" || expectedNonce == "" {
		t.Fatalf("authorization query=%v", authURL.Query())
	}
	destination, err := service.AcceptOAuthCallback(ctx, "apple", state, "apple-code", "", "")
	if err != nil {
		t.Fatal(err)
	}
	returned, _ := url.Parse(destination)
	if returned.Scheme+"://"+returned.Host+returned.Path != frontend || returned.Query().Get("state") != state || returned.Query().Get("code") != "apple-code" {
		t.Fatalf("bridge destination=%q", destination)
	}
	if _, err := service.AcceptOAuthCallback(ctx, "apple", state, "apple-code", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("replayed bridge=%v", err)
	}
	session, err := service.CompleteOAuth(ctx, "apple", state, "apple-code")
	if err != nil {
		t.Fatal(err)
	}
	if exchangeRedirect != bridge || session.User.Email != "apple-e2e@example.test" {
		t.Fatalf("exchange redirect=%q user=%#v", exchangeRedirect, session.User)
	}

	cancel, err := service.BeginOAuthWithProviderRedirect(ctx, "apple", frontend, bridge, "apple-cancel-network")
	if err != nil {
		t.Fatal(err)
	}
	cancelURL, _ := url.Parse(cancel.AuthorizationURL)
	cancelState := cancelURL.Query().Get("state")
	if _, err := service.AcceptOAuthCallback(ctx, "apple", cancelState, "", "access_denied", "Cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteOAuth(ctx, "apple", cancelState, "code"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cancelled state completion=%v", err)
	}

	expired, err := service.BeginOAuthWithProviderRedirect(ctx, "apple", frontend, bridge, "apple-expired-network")
	if err != nil {
		t.Fatal(err)
	}
	expiredURL, _ := url.Parse(expired.AuthorizationURL)
	expiredState := expiredURL.Query().Get("state")
	if _, err := service.pool.Exec(ctx, `update pgbase_meta.user_oauth_states set expires_at=CURRENT_TIMESTAMP-interval '1 second' where provider='apple' and state_hash=$1`, service.oauthStateHash("apple", expiredState)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptOAuthCallback(ctx, "apple", expiredState, "code", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expired state callback=%v", err)
	}
}
