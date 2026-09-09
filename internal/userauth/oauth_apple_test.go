package userauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAppleProviderVerifiesIDTokenAndNonce(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier := "apple-verifier"
	nonceBytes := sha256.Sum256([]byte(verifier))
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes[:])
	var issuer string
	claims := map[string]any{"sub": "apple-user", "email": "apple@example.test", "email_verified": "true", "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix(), "nonce": nonce}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{rsaJWK("current", &key.PublicKey)}})
		case "/token":
			claims["iss"] = issuer
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": signTestJWT(t, key, "current", claims)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: "apple", AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/keys", ClientID: "client", ClientSecret: "signed-client-secret", Scopes: []string{"email"}, HTTPClient: server.Client(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := p.AuthorizationURL("state", nonce, "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	if parsed.Query().Get("nonce") != nonce || parsed.Query().Get("response_mode") != "form_post" {
		t.Fatalf("authorization query = %v", parsed.Query())
	}
	identity, err := p.Exchange(context.Background(), "code", verifier, "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "apple-user" || identity.Email != "apple@example.test" || !identity.EmailVerified {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestAppleProviderRejectsInvalidClaimsAndSignature(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{rsaJWK("key", &key.PublicKey)}})
	}))
	defer server.Close()
	p, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: "apple", AuthorizationURL: server.URL + "/authorize", TokenURL: server.URL + "/token", UserInfoURL: server.URL, ClientID: "client", ClientSecret: "secret", HTTPClient: server.Client(), AllowInsecureLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"sub": "u", "iss": server.URL, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": "n"}
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		signing *rsa.PrivateKey
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://attacker.test" }, key},
		{"wrong audience", func(c map[string]any) { c["aud"] = "other" }, key},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, key},
		{"not active", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, key},
		{"tampered signature", func(map[string]any) {}, other},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := cloneClaims(base)
			test.mutate(claims)
			if _, err := p.verifyAppleIDToken(context.Background(), signTestJWT(t, test.signing, "key", claims)); err == nil {
				t.Fatal("accepted invalid token")
			}
		})
	}
	claims := cloneClaims(base)
	claims["nonce"] = "wrong"
	if _, err := p.exchangeAppleIdentity(context.Background(), signTestJWT(t, key, "key", claims), "verifier"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("nonce error = %v", err)
	}
}

func rsaJWK(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
}
func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}
func cloneClaims(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
